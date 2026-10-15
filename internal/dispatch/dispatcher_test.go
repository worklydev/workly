package dispatch

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/worklydev/workly/internal/signing"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/store/postgres"
	"github.com/worklydev/workly/internal/store/sqlite"
	"github.com/worklydev/workly/internal/store/storetest"
	"github.com/worklydev/workly/internal/workspace"
)

var secret = []byte("test-secret")

// forEachStore runs fn against SQLite, and Postgres when available.
func forEachStore(t *testing.T, fn func(t *testing.T, open func() store.Store)) {
	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "workly.db")
		fn(t, func() store.Store {
			s, err := sqlite.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		})
	})
	t.Run("postgres", func(t *testing.T) {
		url := storetest.PostgresURL(t)
		fn(t, func() store.Store {
			s, err := postgres.Open(t.Context(), url)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		})
	})
}

func setup(t *testing.T, open func() store.Store) store.Store {
	t.Helper()
	s := open()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.EnsureWorkspace(t.Context(), workspace.Default, "Default"); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	return s
}

// putQueue creates a queue with short backoffs so retries happen within a test.
func putQueue(t *testing.T, s store.Store, name string, mutate func(*store.QueueConfig)) {
	t.Helper()
	cfg := store.DefaultQueueConfig()
	cfg.MinBackoff, cfg.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	if mutate != nil {
		mutate(&cfg)
	}
	if _, _, err := s.PutQueue(t.Context(), workspace.Default, name, cfg); err != nil {
		t.Fatalf("PutQueue: %v", err)
	}
}

func enqueue(t *testing.T, s store.Store, nt store.NewTask) store.Task {
	t.Helper()
	nt.Queue = cmp.Or(nt.Queue, "q")
	nt.Method = cmp.Or(nt.Method, "POST")
	if nt.RunAt.IsZero() {
		nt.RunAt = time.Now()
	}
	task, _, err := s.CreateTask(t.Context(), workspace.Default, nt, true)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

type testOptions func(*Options)

// start runs a dispatcher until the test ends (or stop is called) and
// returns a function that stops it and waits for Run to return.
func start(t *testing.T, s store.Store, opts ...testOptions) (stop func()) {
	t.Helper()
	o := Options{
		Store:               s,
		Logger:              slog.New(slog.DiscardHandler),
		SigningSecret:       secret,
		UserAgent:           "Workly/test",
		PollInterval:        50 * time.Millisecond,
		ReapInterval:        50 * time.Millisecond,
		LeaderRetryInterval: 50 * time.Millisecond,
		LeaseGrace:          time.Millisecond,
		ShutdownTimeout:     5 * time.Second,
	}
	for _, fn := range opts {
		fn(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := New(o).Run(ctx); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-stopped:
			case <-time.After(10 * time.Second):
				t.Error("dispatcher did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitForState(t *testing.T, s store.Store, id string, want store.TaskState) store.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		task, err := s.GetTask(context.Background(), workspace.Default, id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if task.State == want {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s state = %s after 10s, want %s", id, task.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func outcomes(t *testing.T, s store.Store, id string) []store.Outcome {
	t.Helper()
	attempts, err := s.ListAttempts(context.Background(), workspace.Default, id)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	var out []store.Outcome
	for _, a := range attempts {
		out = append(out, a.Outcome)
	}
	return out
}

func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDelivery(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)

		type received struct {
			method string
			header http.Header
			body   string
		}
		got := make(chan received, 1)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got <- received{r.Method, r.Header.Clone(), string(body)}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"sent":true}`)
		}))
		defer endpoint.Close()

		task := enqueue(t, s, store.NewTask{
			Queue:        "emails",
			Method:       "PUT",
			URL:          endpoint.URL + "/send",
			Headers:      map[string]string{"Content-Type": "application/json", "X-Tenant": "acme"},
			Body:         []byte(`{"user_id":"123"}`),
			TraceContext: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		})
		start(t, s)

		var r received
		select {
		case r = <-got:
		case <-time.After(10 * time.Second):
			t.Fatal("endpoint never called")
		}
		if r.method != "PUT" || r.body != `{"user_id":"123"}` {
			t.Fatalf("received %s %q", r.method, r.body)
		}
		for k, want := range map[string]string{
			"Content-Type":   "application/json",
			"X-Tenant":       "acme",
			"User-Agent":     "Workly/test",
			"Workly-Task-Id": task.ID,
			"Workly-Queue":   "emails",
			"Workly-Attempt": "1",
			"Traceparent":    task.TraceContext,
		} {
			if v := r.header.Get(k); v != want {
				t.Errorf("header %s = %q, want %q", k, v, want)
			}
		}
		err := signing.Verify(secret, r.header.Get("Workly-Signature"), r.header.Get("Workly-Timestamp"),
			[]byte(r.body), 5*time.Minute, time.Now())
		if err != nil {
			t.Fatalf("signature: %v", err)
		}

		done := waitForState(t, s, task.ID, store.TaskSucceeded)
		if done.AttemptCount != 1 || *done.LastStatus != 200 || done.FinishedAt == nil {
			t.Fatalf("task = %+v", done)
		}
		attempts, _ := s.ListAttempts(t.Context(), workspace.Default, task.ID)
		if len(attempts) != 1 || attempts[0].ResponseBody != `{"sent":true}` ||
			attempts[0].ResponseHeaders["Content-Type"] != "application/json" {
			t.Fatalf("attempts = %+v", attempts)
		}
	})
}

func TestSigningSecretRotation(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		type received struct {
			header http.Header
			body   []byte
		}
		got := make(chan received, 1)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got <- received{r.Header.Clone(), body}
		}))
		defer endpoint.Close()

		previous := []byte("previous-secret")
		enqueue(t, s, store.NewTask{Queue: "emails", Method: "POST", URL: endpoint.URL, Body: []byte("hi")})
		start(t, s, func(o *Options) { o.PreviousSigningSecret = previous })

		var r received
		select {
		case r = <-got:
		case <-time.After(10 * time.Second):
			t.Fatal("endpoint never called")
		}
		// Endpoints on either secret accept the delivery.
		for _, key := range [][]byte{secret, previous} {
			err := signing.Verify(key, r.header.Get("Workly-Signature"), r.header.Get("Workly-Timestamp"),
				r.body, 5*time.Minute, time.Now())
			if err != nil {
				t.Errorf("verify with %q: %v (signature %q)", key, err, r.header.Get("Workly-Signature"))
			}
		}
	})
}

func TestRetries(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", nil)
		putQueue(t, s, "retry4xx", func(c *store.QueueConfig) { c.RetryOn4xx = true; c.MaxAttempts = 2 })
		putQueue(t, s, "three", func(c *store.QueueConfig) { c.MaxAttempts = 3 })

		var flaky atomic.Int32
		mux := http.NewServeMux()
		mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
			if flaky.Add(1) <= 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		})
		mux.HandleFunc("/bad-request", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "missing field", http.StatusBadRequest)
		})
		mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		})
		endpoint := httptest.NewServer(mux)
		defer endpoint.Close()

		one, timeout := 1, 100*time.Millisecond
		flakyTask := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/flaky"})
		badRequest := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/bad-request"})
		badRetried := enqueue(t, s, store.NewTask{Queue: "retry4xx", URL: endpoint.URL + "/bad-request"})
		down := enqueue(t, s, store.NewTask{Queue: "three", URL: endpoint.URL + "/down"})
		slow := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/slow", Timeout: &timeout, MaxAttempts: &one})
		refused := enqueue(t, s, store.NewTask{URL: "http://127.0.0.1:1/", MaxAttempts: &one})
		start(t, s)

		tests := []struct {
			name  string
			task  store.Task
			state store.TaskState
			want  []store.Outcome
		}{
			{"500 twice then 200", flakyTask, store.TaskSucceeded, []store.Outcome{"http_error", "http_error", "success"}},
			{"400 fails without retry", badRequest, store.TaskFailed, []store.Outcome{"http_error"}},
			{"400 retried with retry_on_4xx", badRetried, store.TaskFailed, []store.Outcome{"http_error", "http_error"}},
			{"503 until attempts exhausted", down, store.TaskFailed, []store.Outcome{"http_error", "http_error", "http_error"}},
			{"timeout", slow, store.TaskFailed, []store.Outcome{"timeout"}},
			{"connection refused", refused, store.TaskFailed, []store.Outcome{"network_error"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				task := waitForState(t, s, tt.task.ID, tt.state)
				if got := outcomes(t, s, task.ID); !equal(got, tt.want) {
					t.Fatalf("outcomes = %v, want %v", got, tt.want)
				}
				if task.AttemptCount != len(tt.want) {
					t.Fatalf("attempt_count = %d, want %d", task.AttemptCount, len(tt.want))
				}
			})
		}

		attempts, _ := s.ListAttempts(t.Context(), workspace.Default, badRequest.ID)
		if a := attempts[0]; *a.StatusCode != 400 || a.ResponseBody != "missing field\n" || a.Error != "HTTP 400 Bad Request" {
			t.Fatalf("bad request attempt = %+v", a)
		}
		attempts, _ = s.ListAttempts(t.Context(), workspace.Default, slow.ID)
		if a := attempts[0]; a.StatusCode != nil || a.Error != "no response within 100ms" {
			t.Fatalf("timeout attempt = %+v", a)
		}
	})
}

func TestScheduledTask(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		var deliveredAt atomic.Int64
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deliveredAt.Store(time.Now().UnixMilli())
		}))
		defer endpoint.Close()

		// A long poll interval shows the dispatcher wakes for run_at itself.
		start(t, s, func(o *Options) { o.PollInterval = time.Minute })
		time.Sleep(50 * time.Millisecond) // let it become leader and go to sleep
		runAt := time.Now().Add(300 * time.Millisecond)
		task := enqueue(t, s, store.NewTask{URL: endpoint.URL, RunAt: runAt})

		waitForState(t, s, task.ID, store.TaskSucceeded)
		at := time.UnixMilli(deliveredAt.Load())
		if at.Before(runAt.Truncate(time.Millisecond)) {
			t.Fatalf("delivered at %v, before run_at %v", at, runAt)
		}
		if late := at.Sub(runAt); late > 500*time.Millisecond {
			t.Fatalf("delivered %v after run_at", late)
		}
	})
}

// A dispatcher that dies after claiming a task leaves it running with a
// lease. The next leader's reaper recovers it and delivers it again.
func TestCrashRecovery(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer endpoint.Close()

		// Claim as a crashed dispatcher would have, an hour ago.
		past := time.Now().Add(-time.Hour)
		task := enqueue(t, s, store.NewTask{URL: endpoint.URL, RunAt: past})
		q, _ := s.GetQueue(t.Context(), workspace.Default, "q")
		if claimed, err := s.ClaimTasks(t.Context(), q, 1, past, 0); err != nil || len(claimed) != 1 {
			t.Fatalf("ClaimTasks = %d tasks, %v", len(claimed), err)
		}

		start(t, s)
		done := waitForState(t, s, task.ID, store.TaskSucceeded)
		if got := outcomes(t, s, task.ID); !equal(got, []store.Outcome{"lost", "success"}) {
			t.Fatalf("outcomes = %v, want [lost success]", got)
		}
		if done.AttemptCount != 2 {
			t.Fatalf("attempt_count = %d, want 2", done.AttemptCount)
		}
	})
}

// Shutdown lets in-flight deliveries finish; past ShutdownTimeout they are
// interrupted and recorded as lost, to be retried.
func TestShutdown(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.MinBackoff = time.Hour; c.MaxBackoff = time.Hour })
		release := make(chan struct{})
		started := make(chan string, 2)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- r.URL.Path
			if r.URL.Path == "/slow" {
				time.Sleep(200 * time.Millisecond)
				return
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer endpoint.Close()
		defer close(release)

		slow := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/slow"})
		stuck := enqueue(t, s, store.NewTask{URL: endpoint.URL + "/stuck"})
		stop := start(t, s, func(o *Options) { o.ShutdownTimeout = 500 * time.Millisecond })
		for range 2 {
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("deliveries did not start")
			}
		}

		begin := time.Now()
		stop()
		if elapsed := time.Since(begin); elapsed < 450*time.Millisecond || elapsed > 3*time.Second {
			t.Fatalf("shutdown took %v, want about ShutdownTimeout", elapsed)
		}
		waitForState(t, s, slow.ID, store.TaskSucceeded)
		task := waitForState(t, s, stuck.ID, store.TaskPending)
		if got := outcomes(t, s, stuck.ID); !equal(got, []store.Outcome{"lost"}) {
			t.Fatalf("outcomes = %v, want [lost]", got)
		}
		// Interrupted tasks retry right away, not after the queue's backoff.
		if task.RunAt.After(time.Now()) {
			t.Fatalf("interrupted task run_at = %v, want now", task.RunAt)
		}
	})
}

func TestConcurrentDeliveries(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		const n = 40
		var active, peak atomic.Int32
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cur := active.Add(1)
			defer active.Add(-1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}))
		defer endpoint.Close()

		// The queue allows more than the dispatcher, so MaxInFlight is the limit.
		putQueue(t, s, "q", func(c *store.QueueConfig) { c.MaxConcurrency = 100 })
		var tasks []store.Task
		for i := range n {
			tasks = append(tasks, enqueue(t, s, store.NewTask{URL: endpoint.URL + "/" + strconv.Itoa(i)}))
		}
		start(t, s, func(o *Options) { o.MaxInFlight = 10 })
		for _, task := range tasks {
			waitForState(t, s, task.ID, store.TaskSucceeded)
		}
		if p := peak.Load(); p > 10 || p < 5 {
			t.Fatalf("peak concurrency = %d, want between 5 and MaxInFlight (10)", p)
		}
	})
}

// With two replicas only one leads. Killing the leader's database
// connection (as a crashed pod would) hands leadership to the standby.
func TestFailover(t *testing.T) {
	url := storetest.PostgresURL(t)
	openPG := func() store.Store {
		s, err := postgres.Open(t.Context(), url)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	a := setup(t, openPG)
	b := openPG()

	var hits atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer endpoint.Close()

	start(t, a)
	first := enqueue(t, a, store.NewTask{URL: endpoint.URL})
	waitForState(t, a, first.ID, store.TaskSucceeded)
	start(t, b)

	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	var terminated int
	err = conn.QueryRow(t.Context(), `SELECT count(pg_terminate_backend(pid)) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = 7337 AND objid = hashtext(current_schema())::oid`).Scan(&terminated)
	if err != nil || terminated != 1 {
		t.Fatalf("terminated %d leader connections, err %v; want 1", terminated, err)
	}

	second := enqueue(t, b, store.NewTask{URL: endpoint.URL})
	waitForState(t, b, second.ID, store.TaskSucceeded)
	if h := hits.Load(); h != 2 {
		t.Fatalf("endpoint hit %d times, want 2", h)
	}
}
