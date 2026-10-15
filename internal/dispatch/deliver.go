package dispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/worklydev/workly/internal/signing"
	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

const (
	// maxResponseBody is how much of a response is kept for diagnostics.
	maxResponseBody = 4 << 10
	// maxDrain is how much more is read so the connection can be reused.
	maxDrain = 64 << 10
)

// keptResponseHeaders are stored with each attempt.
var keptResponseHeaders = []string{"Content-Type", "Retry-After", "Location"}

// result is what happened on one delivery.
type result struct {
	started    time.Time
	finished   time.Time
	status     int // 0 if no response
	outcome    store.Outcome
	err        string
	body       string
	headers    map[string]string
	retryAfter time.Duration
}

func (r result) attempt(t store.Task) store.Attempt {
	a := r.record(t.AttemptCount)
	a.TaskID = t.ID
	return a
}

func (r result) runAttempt(run store.Run) store.Attempt {
	a := r.record(run.AttemptCount)
	a.RunID = run.ID
	return a
}

func (r result) record(number int) store.Attempt {
	a := store.Attempt{
		Number:          number,
		StartedAt:       r.started,
		FinishedAt:      r.finished,
		Outcome:         r.outcome,
		Error:           r.err,
		ResponseBody:    r.body,
		ResponseHeaders: r.headers,
	}
	if r.status != 0 {
		a.StatusCode = &r.status
	}
	return a
}

type deliverer struct {
	client *http.Client
	signer Signer
}

// Signer sets the headers every delivery carries. The API uses it too, to
// show a task's delivery request exactly as the dispatcher sends it.
type Signer struct {
	// secrets sign each delivery: the current secret, then the previous
	// one during a rotation. With none, deliveries aren't signed.
	secrets [][]byte
	// lookup, if set, gives each workspace its own secrets instead.
	lookup    SecretsFunc
	userAgent string
}

// SecretsFunc returns the secrets that sign a workspace's deliveries: the
// current one, and the previous one during a rotation (or nil).
type SecretsFunc = func(ctx context.Context, ws workspace.ID) (current, previous []byte, err error)

// NewSigner signs with secret and previousSecret, or, if lookup is set,
// with each workspace's own secrets.
func NewSigner(secret, previousSecret []byte, userAgent string, lookup SecretsFunc) Signer {
	return Signer{secrets: nonEmpty(secret, previousSecret), lookup: lookup, userAgent: userAgent}
}

func nonEmpty(secrets ...[]byte) [][]byte {
	var out [][]byte
	for _, s := range secrets {
		if len(s) > 0 {
			out = append(out, s)
		}
	}
	return out
}

func (s Signer) secretsFor(ctx context.Context, ws workspace.ID) ([][]byte, error) {
	if s.lookup == nil {
		return s.secrets, nil
	}
	current, previous, err := s.lookup(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("signing secret for workspace %s: %w", ws, err)
	}
	return nonEmpty(current, previous), nil
}

// Header returns the headers of attempt number of t, sent at now: the task's
// own, then Workly's.
func (s Signer) Header(ctx context.Context, t store.Task, attempt int, now time.Time) (http.Header, error) {
	secrets, err := s.secretsFor(ctx, t.WorkspaceID)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	for k, v := range t.Headers {
		h.Set(k, v)
	}
	h.Set("User-Agent", s.userAgent)
	h.Set("Workly-Task-Id", t.ID)
	h.Set("Workly-Queue", t.Queue)
	h.Set("Workly-Attempt", strconv.Itoa(attempt))
	sign(h, secrets, t.Body, now)
	if t.TraceContext != "" {
		h.Set("traceparent", t.TraceContext)
	}
	return h, nil
}

// RunHeader returns the headers of attempt number of a schedule run, sent
// at now: the run's own, then Workly's. Runs are told apart from tasks by
// Workly-Run-Id, and carry the slot they are for.
func (s Signer) RunHeader(ctx context.Context, r store.Run, attempt int, now time.Time) (http.Header, error) {
	secrets, err := s.secretsFor(ctx, r.WorkspaceID)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	for k, v := range r.Headers {
		h.Set(k, v)
	}
	h.Set("User-Agent", s.userAgent)
	h.Set("Workly-Run-Id", r.ID)
	h.Set("Workly-Schedule", r.Schedule)
	h.Set("Workly-Scheduled-For", r.ScheduledFor.UTC().Format(time.RFC3339))
	h.Set("Workly-Attempt", strconv.Itoa(attempt))
	sign(h, secrets, r.Body, now)
	return h, nil
}

func sign(h http.Header, secrets [][]byte, body []byte, now time.Time) {
	if len(secrets) > 0 {
		ts := now.Unix()
		h.Set(signing.HeaderTimestamp, strconv.FormatInt(ts, 10))
		h.Set(signing.HeaderSignature, signing.SignAll(secrets, ts, body))
	}
}

// DialControl vets a delivery connection's resolved address before it's
// made; see net.Dialer.Control.
type DialControl = func(network, address string, c syscall.RawConn) error

func newHTTPClient(maxConnsPerHost int, control DialControl) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Keep enough idle connections that concurrent deliveries to one host
	// reuse connections instead of churning through new ones.
	tr.MaxIdleConnsPerHost = maxConnsPerHost
	tr.MaxIdleConns = 4 * maxConnsPerHost
	if control != nil {
		// The same timeouts as http.DefaultTransport's dialer.
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: control}
		tr.DialContext = dialer.DialContext
		// A proxy would connect on our behalf, out of control's sight.
		tr.Proxy = nil
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// deliver sends the task. Cancelling ctx (shutdown) yields OutcomeLost; the
// task timeout yields OutcomeTimeout.
func (d deliverer) deliver(ctx context.Context, t store.Task, timeout time.Duration) result {
	return d.send(ctx, t.Method, t.URL, t.Body, timeout, func(now time.Time) (http.Header, error) {
		return d.signer.Header(ctx, t, t.AttemptCount, now)
	})
}

// deliverRun sends a schedule run, as deliver does a task.
func (d deliverer) deliverRun(ctx context.Context, r store.Run, timeout time.Duration) result {
	return d.send(ctx, r.Method, r.URL, r.Body, timeout, func(now time.Time) (http.Header, error) {
		return d.signer.RunHeader(ctx, r, r.AttemptCount, now)
	})
}

func (d deliverer) send(ctx context.Context, method, url string, body []byte, timeout time.Duration, header func(now time.Time) (http.Header, error)) result {
	r := result{started: time.Now()}

	reqCtx, cancel := context.WithTimeoutCause(ctx, timeout, errTaskTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
	if err != nil {
		r.outcome, r.err = store.OutcomeNetworkError, "invalid request: "+err.Error()
		r.finished = time.Now()
		return r
	}
	if req.Header, err = header(r.started); err != nil {
		// Nothing was sent; the attempt is retried like a network error.
		r.outcome, r.err = store.OutcomeNetworkError, "not sent: "+err.Error()
		r.finished = time.Now()
		return r
	}

	resp, err := d.client.Do(req)
	if err != nil {
		r.outcome, r.err = classifyError(reqCtx, ctx, err, timeout)
		r.finished = time.Now()
		return r
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	_, _ = io.CopyN(io.Discard, resp.Body, maxDrain)
	r.finished = time.Now()

	r.status = resp.StatusCode
	r.body = string(respBody)
	r.headers = map[string]string{}
	for _, h := range keptResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			r.headers[h] = v
		}
	}
	r.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), r.finished)

	switch s := resp.StatusCode; {
	case s >= 200 && s < 300:
		r.outcome = store.OutcomeSuccess
	case s >= 300 && s < 400:
		r.outcome, r.err = store.OutcomeHTTPError, fmt.Sprintf("HTTP %s (redirects are not followed)", resp.Status)
	default:
		r.outcome, r.err = store.OutcomeHTTPError, "HTTP "+resp.Status
	}
	return r
}

var errTaskTimeout = errors.New("delivery timeout")

func classifyError(reqCtx, parent context.Context, err error, timeout time.Duration) (store.Outcome, string) {
	switch {
	case parent.Err() != nil:
		return store.OutcomeLost, "delivery interrupted by dispatcher shutdown"
	case context.Cause(reqCtx) == errTaskTimeout:
		return store.OutcomeTimeout, fmt.Sprintf("no response within %s", timeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return store.OutcomeTimeout, unwrapURLError(err)
	}
	return store.OutcomeNetworkError, unwrapURLError(err)
}

// unwrapURLError drops the `Post "http://…":` prefix; the URL is on the task.
func unwrapURLError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// parseRetryAfter accepts delay-seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}
