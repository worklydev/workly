package dispatch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/signing"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// putSchedule creates a schedule with short backoffs, due at next.
func putSchedule(t *testing.T, s store.Store, name, url string, next time.Time, mutate func(*store.ScheduleConfig)) store.Schedule {
	t.Helper()
	cfg := store.DefaultScheduleConfig()
	cfg.Cron, cfg.URL = "* * * * *", url
	cfg.MinBackoff, cfg.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	if mutate != nil {
		mutate(&cfg)
	}
	sc, _, err := s.PutSchedule(t.Context(), workspace.Default, name, cfg, next)
	if err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}
	return sc
}

// awayFromMinuteEnd waits until a test using an every-minute schedule can
// finish before the next minute's slot fires.
func awayFromMinuteEnd() {
	if sec := time.Now().Second(); sec >= 50 {
		time.Sleep(time.Duration(61-sec) * time.Second)
	}
}

// waitForRuns waits until the schedule has n runs and the newest is in
// state want, and returns them, newest first.
func waitForRuns(t *testing.T, s store.Store, schedule string, n int, want store.RunState) []store.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, _, err := s.ListRuns(context.Background(), workspace.Default, store.RunFilter{Schedule: schedule, Limit: 50})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) == n && runs[0].State == want {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedule %s runs after 10s = %d, newest %+v; want %d, newest %s", schedule, len(runs), runs, n, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScheduleFires(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		awayFromMinuteEnd()
		s := setup(t, open)
		got := make(chan *http.Request, 1)
		bodies := make(chan string, 1)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got <- r
			bodies <- string(body)
		}))
		defer endpoint.Close()

		slot := time.Now().UTC().Truncate(time.Minute)
		putSchedule(t, s, "every-minute", endpoint.URL, slot, func(c *store.ScheduleConfig) {
			c.Headers = map[string]string{"X-Tenant": "acme"}
			c.Body = []byte("ping")
		})
		start(t, s)

		r := <-got
		body := <-bodies
		h := r.Header
		runs := waitForRuns(t, s, "every-minute", 1, store.RunSucceeded)
		run := runs[0]
		if h.Get("Workly-Run-Id") != run.ID || h.Get("Workly-Schedule") != "every-minute" ||
			h.Get("Workly-Scheduled-For") != slot.Format(time.RFC3339) || h.Get("Workly-Attempt") != "1" ||
			h.Get("X-Tenant") != "acme" || h.Get("Workly-Task-Id") != "" || body != "ping" {
			t.Fatalf("delivery headers = %v, body %q", h, body)
		}
		if err := signing.Verify(secret, h.Get(signing.HeaderSignature), h.Get(signing.HeaderTimestamp), []byte(body), time.Minute, time.Now()); err != nil {
			t.Fatalf("signature: %v", err)
		}
		if run.Trigger != store.TriggerSchedule || !run.ScheduledFor.Equal(slot) {
			t.Fatalf("run = %+v", run)
		}
		sc, err := s.GetSchedule(t.Context(), workspace.Default, "every-minute")
		if err != nil || !sc.NextRunAt.Equal(slot.Add(time.Minute)) {
			t.Fatalf("next run = %v, %v; want %v", sc.NextRunAt, err, slot.Add(time.Minute))
		}
	})
}

func TestScheduleRetries(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		awayFromMinuteEnd()
		s := setup(t, open)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer endpoint.Close()

		putSchedule(t, s, "flaky", endpoint.URL, time.Now().UTC().Truncate(time.Minute), func(c *store.ScheduleConfig) {
			c.MaxAttempts = 2
		})
		start(t, s)

		run := waitForRuns(t, s, "flaky", 1, store.RunFailed)[0]
		attempts, err := s.ListRunAttempts(t.Context(), workspace.Default, run.ID)
		if err != nil || len(attempts) != 2 || *attempts[1].StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("attempts = %+v, %v", attempts, err)
		}
	})
}

func TestScheduleMissed(t *testing.T) {
	// A yearly schedule last due two years ago: its latest slot (this New
	// Year) was missed. Only one run is created for the missed slots.
	thisYear := time.Date(time.Now().UTC().Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	if time.Since(thisYear) < 2*missedAfter {
		t.Skip("too close to New Year")
	}
	twoYearsAgo := thisYear.AddDate(-2, 0, 0)
	for _, tt := range []struct {
		missed store.MissedPolicy
		want   store.RunState
	}{
		{store.MissedSkip, store.RunSkipped},
		{store.MissedCatchUp, store.RunSucceeded},
	} {
		t.Run(string(tt.missed), func(t *testing.T) {
			forEachStore(t, func(t *testing.T, open func() store.Store) {
				s := setup(t, open)
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				defer endpoint.Close()
				putSchedule(t, s, "yearly", endpoint.URL, twoYearsAgo, func(c *store.ScheduleConfig) {
					c.Cron, c.Missed = "@yearly", tt.missed
				})
				start(t, s)

				run := waitForRuns(t, s, "yearly", 1, tt.want)[0]
				if !run.ScheduledFor.Equal(thisYear) {
					t.Fatalf("run slot = %v, want %v", run.ScheduledFor, thisYear)
				}
				if tt.want == store.RunSkipped && run.SkipReason != store.SkipMissed {
					t.Fatalf("skip reason = %q", run.SkipReason)
				}
				sc, _ := s.GetSchedule(t.Context(), workspace.Default, "yearly")
				if !sc.NextRunAt.Equal(thisYear.AddDate(1, 0, 0)) {
					t.Fatalf("next run = %v", sc.NextRunAt)
				}
			})
		})
	}
}

func TestScheduleOverlap(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		awayFromMinuteEnd()
		s := setup(t, open)
		release := make(chan struct{})
		started := make(chan struct{}, 2)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-release
		}))
		defer endpoint.Close()
		defer close(release)

		// A manual run is in flight when the slot comes up, so the slot is
		// skipped.
		putSchedule(t, s, "slow", endpoint.URL, time.Now().Add(time.Hour), nil)
		manual, err := s.TriggerSchedule(t.Context(), workspace.Default, "slow", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		start(t, s)
		<-started
		putSchedule(t, s, "slow", endpoint.URL, time.Now().UTC().Truncate(time.Minute), nil)

		runs := waitForRuns(t, s, "slow", 2, store.RunSkipped)
		if runs[0].SkipReason != store.SkipOverlap || runs[1].ID != manual.ID || runs[1].State != store.RunRunning {
			t.Fatalf("runs = %+v", runs)
		}
	})
}

func TestScheduleDoesNotUseTaskSlots(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func() store.Store) {
		s := setup(t, open)
		release := make(chan struct{})
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Workly-Task-Id") != "" {
				<-release
			}
		}))
		defer endpoint.Close()
		defer close(release)

		// Every task slot is taken by a stuck delivery; schedule runs still go.
		enqueue(t, s, store.NewTask{URL: endpoint.URL})
		putSchedule(t, s, "nightly", endpoint.URL, time.Now().Add(time.Hour), nil)
		if _, err := s.TriggerSchedule(t.Context(), workspace.Default, "nightly", time.Now()); err != nil {
			t.Fatal(err)
		}
		start(t, s, func(o *Options) { o.MaxInFlight = 1 })
		waitForRuns(t, s, "nightly", 1, store.RunSucceeded)
	})
}
