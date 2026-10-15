package server_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/worklydev/workly/internal/signing"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/server"
)

func TestQueueLimits(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()

	res := doBody(t, h, "PUT", "/v1/queues/q", "", `{"rate_per_second": 25}`)
	if res.status != 201 || res.json["rate_per_second"] != 25.0 || res.json["max_concurrency"] != 3.0 {
		t.Fatalf("PUT rate = %d %s", res.status, res.body)
	}
	res = doBody(t, h, "PUT", "/v1/queues/q", "", `{"rate_per_second": 0.5, "max_concurrency": 7}`)
	if res.status != 200 || res.json["rate_per_second"] != 0.5 || res.json["max_concurrency"] != 7.0 {
		t.Fatalf("PUT slow rate = %d %s", res.status, res.body)
	}
	// An omitted rate means no limit; concurrency goes back to its default.
	res = doBody(t, h, "PUT", "/v1/queues/q", "", `{}`)
	if _, ok := res.json["rate_per_second"]; ok || res.json["max_concurrency"] != 3.0 {
		t.Errorf("PUT {} = %s, want no rate and max_concurrency 3", res.body)
	}

	for name, body := range map[string]string{
		"rate 0":           `{"rate_per_second": 0}`,
		"negative rate":    `{"rate_per_second": -1}`,
		"rate too high":    `{"rate_per_second": 20000}`,
		"concurrency 0":    `{"max_concurrency": 0}`,
		"concurrency high": `{"max_concurrency": 20000}`,
	} {
		t.Run(name, func(t *testing.T) {
			res := doBody(t, h, "PUT", "/v1/queues/q", "", body)
			if res.status != 400 || errorCode(res) != "invalid_request" {
				t.Fatalf("PUT %s = %d %s", body, res.status, res.body)
			}
		})
	}
}

func TestPauseResumeCancelPending(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	for range 3 {
		doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "http://example.test"}`)
	}
	doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "http://example.test", "delay_seconds": 3600}`)

	res := do(t, h, "POST", "/v1/queues/q/pause", "")
	if res.status != 200 || res.json["paused"] != true {
		t.Fatalf("pause = %d %s", res.status, res.body)
	}
	// Configuring a paused queue keeps it paused.
	if res = doBody(t, h, "PUT", "/v1/queues/q", "", `{}`); res.json["paused"] != true {
		t.Fatalf("PUT paused queue = %d %s", res.status, res.body)
	}
	res = do(t, h, "POST", "/v1/queues/q/cancel-pending", "")
	if res.status != 200 || res.json["cancelled"] != 4.0 {
		t.Fatalf("cancel-pending = %d %s", res.status, res.body)
	}
	if res = do(t, h, "GET", "/v1/queues/q/tasks?state=cancelled", ""); len(res.json["tasks"].([]any)) != 4 {
		t.Fatalf("cancelled tasks after cancel-pending = %s", res.body)
	}
	res = do(t, h, "POST", "/v1/queues/q/resume", "")
	if res.status != 200 || res.json["paused"] != false {
		t.Fatalf("resume = %d %s", res.status, res.body)
	}

	for _, action := range []string{"pause", "resume", "cancel-pending", "retry-failed"} {
		if res := do(t, h, "POST", "/v1/queues/missing/"+action, ""); res.status != 404 || errorCode(res) != "queue_not_found" {
			t.Fatalf("%s missing queue = %d %s", action, res.status, res.body)
		}
	}
}

func TestBatchEnqueue(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true}).Handler()
	doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "http://example.test", "idempotency_key": "old"}`)

	res := doBody(t, h, "POST", "/v1/queues/q/tasks:batch", "", `{"tasks": [
		{"url": "http://example.test/1", "payload": {"n": 1}},
		{"url": "http://example.test/2", "idempotency_key": "old"},
		{"url": "http://example.test/3", "idempotency_key": "new"},
		{"url": "http://example.test/4", "idempotency_key": "new"}
	]}`)
	if res.status != 200 {
		t.Fatalf("batch = %d %s", res.status, res.body)
	}
	results := res.json["results"].([]any)
	var created []bool
	var urls []string
	for _, r := range results {
		r := r.(map[string]any)
		created = append(created, r["created"].(bool))
		urls = append(urls, r["task"].(map[string]any)["url"].(string))
	}
	if fmt.Sprint(created) != "[true false true false]" {
		t.Fatalf("created = %v", created)
	}
	// Duplicates return the existing task.
	if urls[0] != "http://example.test/1" || urls[1] != "http://example.test" || urls[3] != "http://example.test/3" {
		t.Fatalf("urls = %v", urls)
	}

	// One invalid task rejects the batch.
	res = doBody(t, h, "POST", "/v1/queues/q/tasks:batch", "", `{"tasks": [
		{"url": "http://example.test/ok"},
		{"url": "ftp://example.test"}
	]}`)
	if res.status != 400 || !strings.HasPrefix(res.json["error"].(map[string]any)["message"].(string), "tasks[1]: ") {
		t.Fatalf("invalid batch = %d %s", res.status, res.body)
	}
	if res = do(t, h, "GET", "/v1/queues/q/tasks", ""); len(res.json["tasks"].([]any)) != 3 {
		t.Fatalf("tasks after rejected batch = %s, want 3", res.body)
	}

	for name, body := range map[string]string{
		"empty":     `{"tasks": []}`,
		"too many":  `{"tasks": [` + strings.Repeat(`{"url": "http://example.test"},`, 100) + `{"url": "http://example.test"}]}`,
		"no tasks":  `{}`,
		"malformed": `{"tasks": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if res := doBody(t, h, "POST", "/v1/queues/q/tasks:batch", "", body); res.status != 400 {
				t.Fatalf("batch %s = %d %s", name, res.status, res.body)
			}
		})
	}

	strict := newServer(t, server.Options{DisableDispatcher: true, DisableQueueAutoCreate: true}).Handler()
	res = doBody(t, strict, "POST", "/v1/queues/missing/tasks:batch", "", `{"tasks": [{"url": "http://example.test"}]}`)
	if res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("batch to missing queue = %d %s", res.status, res.body)
	}
}

func TestCancelRetry(t *testing.T) {
	st := newStore(t, true)
	h := newServer(t, server.Options{Store: st, DisableDispatcher: true}).Handler()
	enqueue := func() string {
		res := doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "http://example.test"}`)
		return res.json["id"].(string)
	}

	id := enqueue()
	res := do(t, h, "POST", "/v1/tasks/"+id+"/cancel", "")
	if res.status != 200 || res.json["state"] != "cancelled" || res.json["finished_at"] == nil {
		t.Fatalf("cancel = %d %s", res.status, res.body)
	}
	if res = do(t, h, "POST", "/v1/tasks/"+id+"/cancel", ""); res.status != 200 {
		t.Fatalf("second cancel = %d %s", res.status, res.body)
	}
	res = do(t, h, "POST", "/v1/tasks/"+id+"/retry", "")
	if res.status != 200 || res.json["state"] != "pending" || res.json["finished_at"] != nil {
		t.Fatalf("retry = %d %s", res.status, res.body)
	}

	// A task with an attempt in flight can't be retried; once it succeeded
	// it can't be cancelled.
	q, err := st.GetQueue(t.Context(), server.DefaultWorkspace, "q")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimTasks(t.Context(), q, 1, time.Now(), time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimTasks = %d, %v", len(claimed), err)
	}
	if res = do(t, h, "POST", "/v1/tasks/"+id+"/retry", ""); res.status != 409 || errorCode(res) != "invalid_state" {
		t.Fatalf("retry running = %d %s", res.status, res.body)
	}
	finishAttempt(t, st, claimed[0].ID, claimed[0].AttemptCount)
	if res = do(t, h, "POST", "/v1/tasks/"+id+"/cancel", ""); res.status != 409 || errorCode(res) != "invalid_state" {
		t.Fatalf("cancel succeeded = %d %s", res.status, res.body)
	}

	for _, action := range []string{"cancel", "retry"} {
		if res := do(t, h, "POST", "/v1/tasks/task_missing/"+action, ""); res.status != 404 || errorCode(res) != "task_not_found" {
			t.Fatalf("%s missing task = %d %s", action, res.status, res.body)
		}
	}
}

func finishAttempt(t *testing.T, st server.Store, taskID string, number int) {
	t.Helper()
	_, err := st.FinishAttempt(t.Context(), server.DefaultWorkspace, store.AttemptResult{
		Attempt: store.Attempt{
			TaskID: taskID, Number: number, StartedAt: time.Now(), FinishedAt: time.Now(),
			Outcome: store.OutcomeSuccess,
		},
		NextState: store.TaskSucceeded,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
}

func TestRetryFailedTasks(t *testing.T) {
	st := newStore(t, true)
	h := newServer(t, server.Options{Store: st, DisableDispatcher: true}).Handler()
	for range 2 {
		doBody(t, h, "POST", "/v1/queues/q/tasks", "", `{"url": "http://example.test"}`)
	}
	q, err := st.GetQueue(t.Context(), server.DefaultWorkspace, "q")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimTasks(t.Context(), q, 2, time.Now(), time.Minute)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimTasks = %d, %v", len(claimed), err)
	}
	for i, c := range claimed {
		status, errText := 503, "HTTP 503 Service Unavailable"
		if i == 1 {
			status, errText = 422, "HTTP 422 Unprocessable Entity"
		}
		_, err := st.FinishAttempt(t.Context(), server.DefaultWorkspace, store.AttemptResult{
			Attempt: store.Attempt{
				TaskID: c.ID, Number: c.AttemptCount, StartedAt: time.Now(), FinishedAt: time.Now(),
				Outcome: store.OutcomeHTTPError, StatusCode: &status, Error: errText, ResponseBody: `{"error":"nope"}`,
			},
			NextState: store.TaskFailed,
		})
		if err != nil {
			t.Fatalf("FinishAttempt: %v", err)
		}
	}

	res := do(t, h, "GET", "/v1/queues/q", "")
	if failed := res.json["stats"].(map[string]any)["failed"]; failed != 2.0 {
		t.Fatalf("stats.failed = %v, want 2", failed)
	}
	res = do(t, h, "GET", "/v1/queues/q/activity", "")
	if res.status != 200 || res.json["failed"] != 2.0 || res.json["succeeded"] != 0.0 || res.json["retention_seconds"] != nil {
		t.Fatalf("activity = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/queues/q/activity?since="+time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "")
	if res.status != 200 || res.json["failed"] != 0.0 {
		t.Fatalf("activity since the future = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/queues/missing/activity", ""); res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("activity of missing queue = %d %s", res.status, res.body)
	}

	res = do(t, h, "GET", "/v1/queues/q/failures", "")
	groups, _ := res.json["groups"].([]any)
	if res.status != 200 || len(groups) != 2 {
		t.Fatalf("failures = %d %s", res.status, res.body)
	}
	for _, g := range groups {
		g := g.(map[string]any)
		if g["count"] != 1.0 || g["outcome"] != "http_error" || g["response_body"] != `{"error":"nope"}` ||
			g["latest_task_id"] == "" || g["last_failed_at"] == nil {
			t.Fatalf("failure group = %v", g)
		}
	}
	if res := do(t, h, "GET", "/v1/queues/q/failures?limit=51", ""); res.status != 400 {
		t.Fatalf("failures with limit 51 = %d %s", res.status, res.body)
	}
	if res := do(t, h, "GET", "/v1/queues/missing/failures", ""); res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("failures of missing queue = %d %s", res.status, res.body)
	}

	const unavailable = "last_error=HTTP+503+Service+Unavailable"
	for _, path := range []string{"/v1/queues/q/tasks?" + unavailable, "/v1/tasks?queue=q&" + unavailable} {
		res = do(t, h, "GET", path, "")
		tasks := res.json["tasks"].([]any)
		if len(tasks) != 1 {
			t.Fatalf("GET %s = %s, want one task", path, res.body)
		}
		if task := tasks[0].(map[string]any); task["last_outcome"] != "http_error" || task["last_status"] != 503.0 {
			t.Fatalf("task = %v", task)
		}
	}
	res = do(t, h, "POST", "/v1/queues/q/retry-failed?"+unavailable, "")
	if res.status != 200 || res.json["retried"] != 1.0 {
		t.Fatalf("retry-failed by error = %d %s", res.status, res.body)
	}
	res = do(t, h, "POST", "/v1/queues/q/retry-failed", "")
	if res.status != 200 || res.json["retried"] != 1.0 {
		t.Fatalf("retry-failed = %d %s", res.status, res.body)
	}
	res = do(t, h, "GET", "/v1/queues/q/tasks?state=pending", "")
	if n := len(res.json["tasks"].([]any)); n != 2 {
		t.Fatalf("pending tasks after retry-failed = %d, want 2", n)
	}
	if res := do(t, h, "POST", "/v1/queues/missing/retry-failed", ""); res.status != 404 || errorCode(res) != "queue_not_found" {
		t.Fatalf("retry-failed missing queue = %d %s", res.status, res.body)
	}
}

func TestTaskDelivery(t *testing.T) {
	st := newStore(t, true)
	h := newServer(t, server.Options{
		Store: st, DisableDispatcher: true, SigningSecret: "new", PreviousSigningSecret: "old", Version: "1.2.3",
	}).Handler()
	res := doBody(t, h, "POST", "/v1/queues/q/tasks", "",
		`{"url": "http://example.test/hook", "payload": {"b": 1,   "a": 2}, "headers": {"x-tenant": "acme"}}`)
	id := res.json["id"].(string)

	res = do(t, h, "GET", "/v1/tasks/"+id+"/delivery", "")
	if res.status != 200 {
		t.Fatalf("delivery = %d %s", res.status, res.body)
	}
	headers := map[string]string{}
	for k, v := range res.json["headers"].(map[string]any) {
		headers[k] = v.(string)
	}
	body := res.json["body"].(string)
	for k, want := range map[string]string{
		"X-Tenant": "acme", "Content-Type": "application/json", "User-Agent": "Workly/1.2.3",
		"Workly-Task-Id": id, "Workly-Queue": "q", "Workly-Attempt": "1",
	} {
		if headers[k] != want {
			t.Errorf("header %s = %q, want %q", k, headers[k], want)
		}
	}
	if res.json["method"] != "POST" || res.json["url"] != "http://example.test/hook" {
		t.Errorf("request line = %v %v", res.json["method"], res.json["url"])
	}
	// Both secrets sign the exact stored body.
	for _, secret := range []string{"new", "old"} {
		err := signing.Verify([]byte(secret), headers["Workly-Signature"], headers["Workly-Timestamp"],
			[]byte(body), time.Minute, time.Now())
		if err != nil {
			t.Errorf("verify with %q: %v", secret, err)
		}
	}

	if res := do(t, h, "GET", "/v1/tasks/task_missing/delivery", ""); res.status != 404 || errorCode(res) != "task_not_found" {
		t.Fatalf("delivery of missing task = %d %s", res.status, res.body)
	}
}

func TestQueueActivityRetention(t *testing.T) {
	h := newServer(t, server.Options{DisableDispatcher: true, Retention: 48 * time.Hour}).Handler()
	doBody(t, h, "PUT", "/v1/queues/q", "", `{}`)
	res := do(t, h, "GET", "/v1/queues/q/activity", "")
	if res.status != 200 || res.json["retention_seconds"] != 172800.0 {
		t.Fatalf("activity = %d %s", res.status, res.body)
	}
	since, err := time.Parse(time.RFC3339, res.json["since"].(string))
	if err != nil || time.Since(since) < 7*24*time.Hour-time.Minute || time.Since(since) > 7*24*time.Hour+time.Minute {
		t.Fatalf("since = %v, %v; want 7 days ago", res.json["since"], err)
	}
}
