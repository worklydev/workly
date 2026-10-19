package api

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/worklydev/workly/internal/store"
)

var nameRE = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func validateQueueName(name string) error { return validateName("queue", name) }

func validateScheduleName(name string) error { return validateName("schedule", name) }

// CallbackQueue is the queue callbacks go to unless they name one. Only
// Workly enqueues into it.
const CallbackQueue = "_callbacks"

// Reserved reports whether a queue name is reserved for Workly's own queues.
// Queues with such names that users created before they were reserved keep
// working, but no new ones can be created.
func Reserved(queue string) bool { return strings.HasPrefix(queue, "_") }

func validateName(kind, name string) error {
	if !nameRE.MatchString(name) {
		return invalid("%s name %q must be 1-64 lowercase letters, digits, '-' or '_'", kind, name)
	}
	return nil
}

const (
	maxAttemptsLimit = 1000
	minTimeout       = time.Second
	maxTimeout       = 30 * time.Minute
	// maxLimit bounds rate_per_second and max_concurrency.
	maxLimit = 10000
)

// queueConfig applies defaults to omitted fields and checks bounds.
func queueConfig(in QueueConfig) (store.QueueConfig, error) {
	cfg := store.DefaultQueueConfig()
	if in.MaxAttempts != nil {
		cfg.MaxAttempts = *in.MaxAttempts
	}
	if in.MinBackoffSeconds != nil {
		cfg.MinBackoff = duration(*in.MinBackoffSeconds)
	}
	if in.MaxBackoffSeconds != nil {
		cfg.MaxBackoff = duration(*in.MaxBackoffSeconds)
	}
	if in.BackoffMultiplier != nil {
		cfg.BackoffMultiplier = *in.BackoffMultiplier
	}
	if in.TimeoutSeconds != nil {
		cfg.Timeout = duration(*in.TimeoutSeconds)
	}
	if in.RetryOn4xx != nil {
		cfg.RetryOn4xx = *in.RetryOn4xx
	}
	if in.RatePerSecond != nil {
		cfg.RatePerSecond = *in.RatePerSecond
	}
	if in.MaxConcurrency != nil {
		cfg.MaxConcurrency = *in.MaxConcurrency
	}

	switch {
	case cfg.MaxAttempts < 1 || cfg.MaxAttempts > maxAttemptsLimit:
		return cfg, invalid("max_attempts must be between 1 and %d", maxAttemptsLimit)
	case cfg.MinBackoff < 0:
		return cfg, invalid("min_backoff_seconds must not be negative")
	case cfg.MaxBackoff < cfg.MinBackoff:
		return cfg, invalid("max_backoff_seconds must be at least min_backoff_seconds")
	case cfg.BackoffMultiplier < 1:
		return cfg, invalid("backoff_multiplier must be at least 1")
	case cfg.Timeout < minTimeout || cfg.Timeout > maxTimeout:
		return cfg, invalid("timeout_seconds must be between %d and %d", int(minTimeout.Seconds()), int(maxTimeout.Seconds()))
	case in.RatePerSecond != nil && (cfg.RatePerSecond <= 0 || cfg.RatePerSecond > maxLimit):
		return cfg, invalid("rate_per_second must be above 0 and at most %d; omit it for no limit", maxLimit)
	case cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > maxLimit:
		return cfg, invalid("max_concurrency must be between 1 and %d", maxLimit)
	}
	return cfg, nil
}

const (
	maxURLLength            = 2048
	maxIdempotencyKeyLength = 256
	maxHeaders              = 64
)

var methods = []HTTPMethod{"GET", "POST", "PUT", "PATCH", "DELETE"}

// reservedHeaders are set by Workly or by the HTTP client and can't be
// overridden per task. Workly-* is also reserved.
var reservedHeaders = []string{"Host", "Content-Length", "Transfer-Encoding", "Connection", "User-Agent"}

// request is the HTTP request a task or schedule sends, as given.
type request struct {
	url         string
	method      *HTTPMethod
	headers     *map[string]string
	payload     *json.RawMessage
	body        *string
	contentType *string
}

// validRequest is a validated request, with Content-Type set from the body.
type validRequest struct {
	method  string
	url     string
	headers map[string]string
	body    []byte
}

func validateRequest(in request, maxBody int64) (validRequest, error) {
	r := validRequest{method: "POST", url: in.url, headers: map[string]string{}}
	if err := validateURL(in.url); err != nil {
		return r, err
	}
	if in.method != nil {
		if !slices.Contains(methods, *in.method) {
			return r, invalid("method must be one of GET, POST, PUT, PATCH, DELETE")
		}
		r.method = string(*in.method)
	}

	if in.headers != nil {
		if len(*in.headers) > maxHeaders {
			return r, invalid("at most %d headers are allowed", maxHeaders)
		}
		for k, v := range *in.headers {
			if err := validateHeader(k, v); err != nil {
				return r, err
			}
			r.headers[k] = v
		}
	}

	hasPayload := in.payload != nil && string(*in.payload) != "null"
	switch {
	case hasPayload && in.body != nil:
		return r, invalid("use either payload or body, not both")
	case hasPayload:
		r.body = *in.payload
		setContentType(r.headers, "application/json", false)
	case in.body != nil:
		r.body = []byte(*in.body)
		if in.contentType != nil {
			setContentType(r.headers, *in.contentType, true)
		} else {
			setContentType(r.headers, "text/plain; charset=utf-8", false)
		}
	}
	if in.contentType != nil && in.body == nil {
		return r, invalid("content_type applies only to body")
	}
	if int64(len(r.body)) > maxBody {
		return r, payloadTooLarge(maxBody)
	}
	return r, nil
}

// newTask validates an enqueue request.
func newTask(queue string, req EnqueueRequest, traceparent string, maxBody int64, now time.Time) (store.NewTask, error) {
	nt := store.NewTask{Queue: queue}
	r, err := validateRequest(request{req.Url, req.Method, req.Headers, req.Payload, req.Body, req.ContentType}, maxBody)
	if err != nil {
		return nt, err
	}
	nt.Method, nt.URL, nt.Headers, nt.Body = r.method, r.url, r.headers, r.body

	switch {
	case req.RunAt != nil && req.DelaySeconds != nil:
		return nt, invalid("use either run_at or delay_seconds, not both")
	case req.RunAt != nil:
		nt.RunAt = *req.RunAt
	case req.DelaySeconds != nil:
		if *req.DelaySeconds < 0 {
			return nt, invalid("delay_seconds must not be negative")
		}
		nt.RunAt = now.Add(duration(*req.DelaySeconds))
	default:
		nt.RunAt = now
	}

	if req.MaxAttempts != nil {
		if *req.MaxAttempts < 1 || *req.MaxAttempts > maxAttemptsLimit {
			return nt, invalid("max_attempts must be between 1 and %d", maxAttemptsLimit)
		}
		nt.MaxAttempts = req.MaxAttempts
	}
	if req.TimeoutSeconds != nil {
		d := duration(*req.TimeoutSeconds)
		if d < minTimeout || d > maxTimeout {
			return nt, invalid("timeout_seconds must be between %d and %d", int(minTimeout.Seconds()), int(maxTimeout.Seconds()))
		}
		nt.Timeout = &d
	}
	if req.IdempotencyKey != nil {
		if len(*req.IdempotencyKey) > maxIdempotencyKeyLength {
			return nt, invalid("idempotency_key must be at most %d bytes", maxIdempotencyKeyLength)
		}
		nt.IdempotencyKey = *req.IdempotencyKey
	}
	if req.GroupId != nil {
		if *req.GroupId == "" || len(*req.GroupId) > maxIdempotencyKeyLength {
			return nt, invalid("group_id must be a group's ID")
		}
		nt.GroupID = *req.GroupId
	}
	// Only keep a well-formed traceparent; it's forwarded verbatim.
	if validHeaderValue(traceparent) && len(traceparent) <= 512 {
		nt.TraceContext = traceparent
	}
	return nt, nil
}

func validateURL(raw string) error {
	if len(raw) > maxURLLength {
		return invalid("url must be at most %d characters", maxURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("url must be an absolute http or https URL")
	}
	return nil
}

func validateHeader(name, value string) error {
	if !validHeaderName(name) {
		return invalid("invalid header name %q", name)
	}
	if !validHeaderValue(value) {
		return invalid("header %q has an invalid value", name)
	}
	if strings.HasPrefix(strings.ToLower(name), "workly-") {
		return invalid("header %q is reserved: Workly-* headers are set by Workly", name)
	}
	for _, r := range reservedHeaders {
		if strings.EqualFold(name, r) {
			return invalid("header %q is set by the HTTP client and can't be set", name)
		}
	}
	return nil
}

// validHeaderName checks for an RFC 9110 token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// validHeaderValue rejects control characters other than tab.
func validHeaderValue(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < ' ' && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

// setContentType sets Content-Type, keeping one the caller set in headers
// unless override is set.
func setContentType(headers map[string]string, value string, override bool) {
	for k := range headers {
		if strings.EqualFold(k, "Content-Type") {
			if !override {
				return
			}
			delete(headers, k)
		}
	}
	headers["Content-Type"] = value
}
