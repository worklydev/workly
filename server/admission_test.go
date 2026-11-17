package server_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/worklydev/workly/server"
)

// quota admits up to limit tasks and schedules running at most every
// minInterval.
type quota struct {
	mu          sync.Mutex
	limit       int
	admitted    int
	schedules   []server.ScheduleAdmission
	minInterval time.Duration
}

func (q *quota) AdmitTasks(_ context.Context, _ server.WorkspaceID, n int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.admitted+n > q.limit {
		return server.LimitError(http.StatusForbidden, "quota_exceeded", "monthly task quota reached")
	}
	q.admitted += n
	return nil
}

func (q *quota) AdmitSchedule(_ context.Context, _ server.WorkspaceID, s server.ScheduleAdmission) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.schedules = append(q.schedules, s)
	if s.Interval < q.minInterval {
		return server.LimitError(http.StatusForbidden, "limit_exceeded", "schedules may run every 15 minutes at most")
	}
	return nil
}

func TestAdmissionTasks(t *testing.T) {
	q := &quota{limit: 3}
	h := newServer(t, server.Options{Admission: q}).Handler()

	if r := doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "https://example.com/hook"}`); r.status != 201 || q.admitted != 1 {
		t.Fatalf("enqueue = %d %s, admitted %d", r.status, r.body, q.admitted)
	}
	batch := `{"tasks": [{"url": "https://example.com/1"}, {"url": "https://example.com/2"}]}`
	if r := doBody(t, h, "POST", "/v1/queues/q/tasks:batch", "", batch); r.status != 200 || q.admitted != 3 {
		t.Fatalf("batch = %d %s, admitted %d", r.status, r.body, q.admitted)
	}
	r := doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "https://example.com/4"}`)
	if r.status != 403 || r.json["error"].(map[string]any)["code"] != "quota_exceeded" {
		t.Fatalf("over quota = %d %s", r.status, r.body)
	}
}

func TestAdmissionSchedules(t *testing.T) {
	q := &quota{minInterval: 15 * time.Minute}
	h := newServer(t, server.Options{Admission: q}).Handler()

	put := func(cron string) response {
		return doBody(t, h, "PUT", "/v1/schedules/nightly", "",
			`{"cron": "`+cron+`", "url": "https://example.com/nightly"}`)
	}
	if r := put("0 3 * * *"); r.status != 201 {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	if r := put("*/5 * * * *"); r.status != 403 {
		t.Fatalf("every 5 minutes = %d %s", r.status, r.body)
	}
	// The shortest gap counts, not the average: 10 minutes, then 23h50m.
	if r := put("0,10 3 * * *"); r.status != 403 {
		t.Fatalf("10 minutes apart once a day = %d %s", r.status, r.body)
	}
	want := []server.ScheduleAdmission{
		{Name: "nightly", Exists: false, Interval: 24 * time.Hour},
		{Name: "nightly", Exists: true, Interval: 5 * time.Minute, PreviousInterval: 24 * time.Hour},
		{Name: "nightly", Exists: true, Interval: 10 * time.Minute, PreviousInterval: 24 * time.Hour},
	}
	if len(q.schedules) != len(want) {
		t.Fatalf("admissions = %+v", q.schedules)
	}
	for i := range want {
		if q.schedules[i] != want[i] {
			t.Errorf("admission %d = %+v, want %+v", i, q.schedules[i], want[i])
		}
	}
}
