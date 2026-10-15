// Command loadtest measures Workly's sustained throughput and latency.
//
// It runs a no-op delivery endpoint, enqueues tasks to a running Workly
// server at a fixed rate, one request per task as a typical client would,
// and reports enqueue latency, end-to-end latency (enqueue to delivery) and
// throughput:
//
//	go run ./tools/loadtest -rate 1000 -duration 60s
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	api := flag.String("api", "http://localhost:7337", "Workly API base URL")
	apiKey := flag.String("api-key", os.Getenv("WORKLY_API_KEY"), "API key, if the server requires one")
	queue := flag.String("queue", "loadtest", "queue to use; it is deleted and recreated")
	rate := flag.Int("rate", 1000, "tasks enqueued per second")
	duration := flag.Duration("duration", 30*time.Second, "how long to enqueue for")
	workers := flag.Int("workers", 64, "concurrent enqueue requests")
	drainTimeout := flag.Duration("drain-timeout", time.Minute, "how long to wait for deliveries after enqueueing stops")
	flag.Parse()

	lt := &loadTest{api: *api, apiKey: *apiKey, queue: *queue}
	if err := lt.run(*rate, *duration, *workers, *drainTimeout); err != nil {
		log.Fatal(err)
	}
}

type loadTest struct {
	api, apiKey, queue string
	client             *http.Client

	mu         sync.Mutex
	enqueueLat []time.Duration
	deliverLat []time.Duration
	seen       map[int]bool
	duplicates int
	enqueueErr int
	delivered  atomic.Int64
}

func (lt *loadTest) run(rate int, duration time.Duration, workers int, drainTimeout time.Duration) error {
	lt.client = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: workers}, Timeout: 10 * time.Second}
	lt.seen = map[int]bool{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	target := &http.Server{Handler: http.HandlerFunc(lt.receive)}
	go target.Serve(ln)
	defer target.Close()
	targetURL := "http://" + ln.Addr().String() + "/noop"

	// Start from an empty queue with default settings, except that only the
	// dispatcher's global cap limits deliveries in flight.
	if err := lt.call("DELETE", "/v1/queues/"+lt.queue, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
		return err
	}
	if err := lt.call("PUT", "/v1/queues/"+lt.queue, map[string]any{"max_concurrency": 10000}, http.StatusCreated); err != nil {
		return err
	}

	total := int(float64(rate) * duration.Seconds())
	fmt.Printf("enqueueing %d tasks at %d/s for %v with %d workers\n", total, rate, duration, workers)

	jobs := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for n := range jobs {
				lt.enqueue(targetURL, n)
			}
		})
	}
	// Pace by schedule rather than a ticker, so a slow moment is caught up
	// instead of lowering the rate.
	start := time.Now()
	interval := time.Second / time.Duration(rate)
	for n := range total {
		if d := time.Until(start.Add(time.Duration(n) * interval)); d > 0 {
			time.Sleep(d)
		}
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	enqueueTime := time.Since(start)

	lt.mu.Lock()
	enqueued := total - lt.enqueueErr
	lt.mu.Unlock()
	deadline := time.Now().Add(drainTimeout)
	for lt.delivered.Load() < int64(enqueued) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	totalTime := time.Since(start)

	lt.mu.Lock()
	defer lt.mu.Unlock()
	delivered := len(lt.seen)
	fmt.Printf("\nenqueue:  %d ok, %d errors in %v (%.0f/s)\n", enqueued, lt.enqueueErr, enqueueTime.Round(time.Millisecond), float64(enqueued)/enqueueTime.Seconds())
	fmt.Printf("          latency %s\n", percentiles(lt.enqueueLat))
	fmt.Printf("delivery: %d delivered, %d duplicates, %d missing; all done %v after start (%.0f/s)\n",
		delivered, lt.duplicates, enqueued-delivered, totalTime.Round(time.Millisecond), float64(delivered)/totalTime.Seconds())
	fmt.Printf("          enqueue-to-delivery latency %s\n", percentiles(lt.deliverLat))
	if delivered < enqueued {
		return fmt.Errorf("%d tasks not delivered within %v", enqueued-delivered, drainTimeout)
	}
	return nil
}

type payload struct {
	N int `json:"n"`
	// T is when the task was enqueued, in Unix nanoseconds. The endpoint
	// runs in this process, so both ends use the same clock.
	T int64 `json:"t"`
}

func (lt *loadTest) enqueue(targetURL string, n int) {
	start := time.Now()
	err := lt.call("POST", "/v1/queues/"+lt.queue+"/tasks", map[string]any{
		"url":     targetURL,
		"payload": payload{N: n, T: start.UnixNano()},
	}, http.StatusCreated)
	lat := time.Since(start)

	lt.mu.Lock()
	defer lt.mu.Unlock()
	if err != nil {
		if lt.enqueueErr == 0 {
			log.Printf("enqueue failed: %v", err)
		}
		lt.enqueueErr++
		return
	}
	lt.enqueueLat = append(lt.enqueueLat, lat)
}

func (lt *loadTest) receive(w http.ResponseWriter, r *http.Request) {
	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	lat := time.Since(time.Unix(0, p.T))
	lt.mu.Lock()
	if lt.seen[p.N] {
		lt.duplicates++
	} else {
		lt.seen[p.N] = true
		lt.deliverLat = append(lt.deliverLat, lat)
		lt.delivered.Add(1)
	}
	lt.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (lt *loadTest) call(method, path string, body any, okStatus ...int) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, lt.api+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if lt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+lt.apiKey)
	}
	resp, err := lt.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if !slices.Contains(okStatus, resp.StatusCode) {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, respBody)
	}
	return nil
}

func percentiles(ds []time.Duration) string {
	if len(ds) == 0 {
		return "n/a"
	}
	slices.Sort(ds)
	at := func(p float64) time.Duration {
		return ds[min(len(ds)-1, int(p*float64(len(ds))))].Round(100 * time.Microsecond)
	}
	return fmt.Sprintf("p50 %v, p95 %v, p99 %v, max %v", at(0.50), at(0.95), at(0.99), ds[len(ds)-1].Round(100*time.Microsecond))
}
