# Concepts

This page explains how Workly thinks about work: queues, tasks and attempts, what happens on each delivery, and how your endpoint can trust a request came from Workly. [Schedules](schedules.md) and [task groups](groups.md) build on the same pieces and have pages of their own.

## Queues

A **queue** holds tasks and the policy for delivering them. It's created the first time you enqueue into it, with the defaults below, and you change its policy with `PUT /v1/queues/{queue}` (or `workly queues put`, or a [`workly.yaml`](deploy.md)).

| Setting | Default | |
|---|---|---|
| `max_attempts` | 5 | Total attempts before a task fails. |
| `min_backoff_seconds`, `max_backoff_seconds`, `backoff_multiplier` | 2, 3600, 2 | The wait between attempts (see [Retries](#retries)). |
| `timeout_seconds` | 30 | How long one attempt may take. |
| `max_concurrency` | 3 | Deliveries in flight at once, up to 10,000. |
| `rate_per_second` | none | Deliveries started per second. |
| `retry_on_4xx` | false | Retry 4xx responses other than 408 and 429, which are always retried. |

The full list is in the [API reference](../api/openapi.yaml). Queue names are lowercase letters, digits, `-` and `_`; names starting with `_` are Workly's own.

A **paused** queue keeps accepting tasks but doesn't start deliveries; those already in flight finish. Workly shares deliveries fairly between queues, so a busy queue doesn't hold up a quiet one.

## Tasks

A **task** is one HTTP request Workly owes your endpoint: a URL, a method (`POST` by default), headers, and a body: either `payload`, sent as JSON, or `body` with its own `content_type`. It can wait: `delay_seconds` or `run_at` set when it's first due. It can override its queue's `max_attempts` and `timeout_seconds`, and with `on_complete` call you back each time it finishes (see [Callbacks](callbacks.md)).

A task is in one of these states:

| State | |
|---|---|
| `pending` | Waiting to be delivered: due now, waiting for its `run_at`, or waiting to be retried. |
| `running` | An attempt is in flight. |
| `succeeded` | An attempt got a 2xx response. |
| `failed` | It used up its attempts, or got a response that isn't retried. |
| `cancelled` | Cancelled before it succeeded or failed. A running task's current attempt still finishes and is recorded, but isn't retried. |

`failed` isn't the end: once the endpoint is fixed, retry one task (`POST /v1/tasks/{id}/retry`) or every failure in a queue (`workly queues retry-failed`), and it gets a fresh set of attempts. Finished tasks are kept, with their attempts, for `WORKLY_RETENTION` (7 days by default), then deleted.

### Idempotency keys

Give a task an `idempotency_key` and enqueueing it again in the same queue returns the existing task (`200` instead of `201`) instead of creating a second one. A key holds as long as its task is kept, so retrying an enqueue after a timeout or a crash is safe. This deduplicates enqueues; deduplicating deliveries is your handler's job (see [Delivery semantics](#delivery-semantics)).

## Attempts

Each delivery of a task is an **attempt**, recorded with its start and end, duration, status code, response headers, the first 4 KB of the response body, and how it ended:

| Outcome | |
|---|---|
| `success` | A 2xx response. |
| `http_error` | Any other response. |
| `timeout` | No response within `timeout_seconds`. |
| `network_error` | The connection failed: DNS, refused, reset, TLS. |
| `lost` | Workly stopped mid-delivery, so the result is unknown. The endpoint may have done the work. |

This history is what the dashboard, the CLI and [MCP](mcp.md) show when you ask why a task failed.

### Retries

- **Retried:** 5xx, 408 and 429 responses, timeouts, network errors and lost attempts.
- **Not retried:** other 4xx responses fail the task at once, since sending the same request again won't change the answer. Set `retry_on_4xx` on the queue if your endpoint uses them for temporary problems.
- **Backoff:** retry *n* waits a random time between `min_backoff_seconds` and `min_backoff_seconds × backoff_multiplier^(n−1)`, capped at `max_backoff_seconds`. The randomness (jitter) keeps a burst of failures from coming back all at once. A `Retry-After` header on the response is honoured.

## Delivery semantics

Delivery is **at least once**. Workly never drops a task, but your endpoint can see one more than once:

- Workly can stop between sending a request and recording the response (a crash, a deploy, a lost connection to the database). The attempt is recorded as `lost` and the task is delivered again.
- Your endpoint can do the work and then time out, or fail to answer. Workly can't tell that apart from not doing it.

So make handlers safe to run twice. Every delivery carries headers to help:

| Header | |
|---|---|
| `Workly-Task-Id` | The task, the same on every attempt. Use it as a deduplication key. |
| `Workly-Queue` | The task's queue. |
| `Workly-Attempt` | The attempt number, from 1. |
| `Workly-Timestamp`, `Workly-Signature` | The signature (see [Signing](#signing)). |
| `traceparent` | The trace context of the request that enqueued the task, if it had one, so your handler's spans join that trace. |

Your own `headers` are sent too; `Workly-*` names are reserved. Schedule runs carry `Workly-Run-Id`, `Workly-Schedule` and `Workly-Scheduled-For` instead of the task headers ([schedules](schedules.md#handling-a-run)), and a [workflow](workflows.md#what-a-step-receives)'s steps carry `Workly-Workflow`, `Workly-Workflow-Run-Id`, `Workly-Workflow-Step` and `Workly-Workflow-Step-Id` as well.

Order isn't guaranteed. Tasks in a queue are started roughly in the order they're due, but with more than one delivery at a time, and with retries, they can finish in any order. If order matters, set `max_concurrency: 1`, and keep in mind that a retry still lets later tasks go first.

## Signing

Every delivery is signed with a secret that Workly and your endpoint share (`WORKLY_SIGNING_SECRET` when you self-host; the API keys page on workly.run). Check the signature before trusting a request: anyone who can reach your endpoint can call it, but only Workly knows the secret.

The [SDKs](quickstart.md#handle-it) do the check for you, with `verify_signature` in Python and `verifySignature` in TypeScript. In another language it's a few lines.

### Verifying a signature without an SDK

A delivery carries two headers:

```text
Workly-Timestamp: 1790000000
Workly-Signature: v1=00f7255c88a43b3e2d47b211ff8e71cf935601cabff5e7adad0de2cc72a4e3c6
```

The signature is the hex HMAC-SHA256, keyed with the secret, of the timestamp, a `.`, and the request body exactly as it arrived:

```text
v1=hex(HMAC-SHA256(secret, "<Workly-Timestamp>.<body>"))
```

To verify a request:

1. Read the **raw body**, before any JSON parsing: re-serializing it changes the bytes and breaks the signature.
2. Compute the HMAC of `timestamp + "." + body` with your secret.
3. Split `Workly-Signature` on commas. It holds more than one signature while the secret is being [rotated](configuration.md#rotating-the-signing-secret); accept the request if any `v1=` value matches. Compare in constant time.
4. Reject the request if the timestamp is more than 5 minutes from your clock. That stops someone who captured a request from replaying it later. The SDKs use the same tolerance.

The timestamp is the time of the attempt, so every retry has a fresh one and a new signature.

For a test vector: with the secret `secret`, the timestamp `1790000000` and the body `{"user_id":"123"}`, the signature is the one above. You can check it from a shell:

```sh
printf '%s' '1790000000.{"user_id":"123"}' | openssl dgst -sha256 -hmac secret
```

In Go:

```go
func verify(secret []byte, r *http.Request, body []byte) bool {
	ts, err := strconv.ParseInt(r.Header.Get("Workly-Timestamp"), 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	want := mac.Sum(nil)
	for _, part := range strings.Split(r.Header.Get("Workly-Signature"), ",") {
		sig, ok := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		got, err := hex.DecodeString(sig)
		if ok && err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}
```

In Ruby (3.0 or later):

```ruby
require "openssl"

def verify(secret, headers, body)
  ts = headers["Workly-Timestamp"].to_s
  return false unless ts.match?(/\A\d+\z/) && (Time.now.to_i - ts.to_i).abs <= 300
  want = "v1=" + OpenSSL::HMAC.hexdigest("SHA256", secret, "#{ts}.#{body}")
  headers["Workly-Signature"].to_s.split(",").any? { |sig| OpenSSL.secure_compare(sig.strip, want) }
end
```

Answer a request that doesn't verify with a 401. Workly doesn't retry 4xx responses, so a wrong secret shows up as failed tasks with a 401 in the dashboard instead of retrying for an hour.
