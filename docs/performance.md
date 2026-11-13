# Performance

These are measurements on one laptop, to show where Workly's limits are and what moves them. On Postgres, Workly enqueues about 15,000 tasks/s sent one per request and about 60,000 tasks/s sent in batches, and delivers about 4,000 tasks/s to a fast endpoint. SQLite enqueues about 20,000 tasks/s and delivers about 6,000.

## Results

Each run enqueues tasks for 20 s to a no-op endpoint that answers `204`, after a warm-up run against a fresh database. Latency is measured from just before the enqueue request to the moment the endpoint receives the delivery. Repeated runs vary by up to about 20%, and more near a limit.

### Enqueueing

With the queue paused, nothing is delivered, so these runs measure enqueues alone: as many requests as the load test can send, either one task per request (`POST /v1/queues/{queue}/tasks`) or 100 per request (`POST /v1/queues/{queue}/tasks:batch`).

| Requests | SQLite | Postgres |
|---|---|---|
| 1 task each, 64 at once | 19,720/s (p50 3.1 ms, p99 6.2 ms) | 14,990/s (p50 3.9 ms, p99 12 ms) |
| 100 tasks each, 4 at once | 20,280/s (p50 15 ms, p99 64 ms) | 37,100/s (p50 9.6 ms, p99 36 ms) |
| 100 tasks each, 16 at once | 19,790/s (p50 60 ms, p99 360 ms) | 56,750/s (p50 25 ms, p99 75 ms) |
| 100 tasks each, 64 at once | 19,840/s (p50 227 ms, p99 1.4 s) | 60,950/s (p50 98 ms, p99 278 ms) |

Latencies are per request. Enqueues that arrive while others are being written share the next statement, so single enqueues from many clients are written much like a batch. On Postgres, batches still enqueue about 4 times as many tasks per second: if you enqueue many tasks at once, send them in batches. SQLite writes about 20,000 tasks/s on its one connection either way, so more concurrent batches only add waiting.

### Enqueue and delivery

One task per request at a fixed rate, with 64 concurrent requests, as a typical client sends them.

**SQLite** (`workly dev`):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 2,000/s | 1.0 ms / 6.1 ms | 2.3 ms / 4.5 ms / 11 ms | 1,999/s, all |
| 4,000/s | 2.1 ms / 18 ms | 4.6 ms / 25 ms / 80 ms | 3,996/s, all |
| 5,000/s | 2.2 ms / 11 ms | 5.1 ms / 14 ms / 66 ms | 4,995/s, all |
| 6,000/s | 3.5 ms / 14 ms | 8.6 ms / 320 ms / 382 ms | 5,993/s, all (at the limit) |
| 8,000/s | 6.1 ms / 18 ms | backlog grows | ~6,190/s (saturated) |

**Postgres** (`workly serve`, default connection pool):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 1,000/s | 1.1 ms / 8.0 ms | 2.6 ms / 4.7 ms / 16 ms | 999/s, all |
| 2,000/s | 1.7 ms / 21 ms | 4.7 ms / 17 ms / 79 ms | 1,998/s, all |
| 3,000/s | 2.2 ms / 17 ms | 7.5 ms / 74 ms / 213 ms | 2,998/s, all |
| 4,000/s | 2.4 ms / 10 ms | 7.2 ms / 30 ms / 68 ms | 3,995/s, all (at the limit) |
| 6,000/s | 3.5 ms / 17 ms | backlog grows | ~4,510/s (saturated) |

Around the Postgres limit, runs differ: another run at 4,000/s fell behind and delivered 3,270/s, and one at 5,000/s kept up.

No run lost or duplicated a task.

### Test setup

Apple M1 Pro (10 cores, 16 GB), macOS. Postgres 17 runs in Docker Desktop on the same machine, so every query crosses the Docker VM network (roughly 0.5 ms per round trip). Workly, the load generator and the endpoint all share the machine. Workly logs every attempt at info level, as it does by default. Dedicated Postgres on Linux should do better; treat these numbers as a floor.

## What limits throughput

- **The dispatch loop.** One loop claims tasks and one writer records results, each a statement or two per batch. On Postgres they saturate at about 4,000–4,500 deliveries/s here.
- **Enqueue statements.** Each replica runs at most 4 enqueue statements at once on Postgres, and 1 on SQLite. Enqueues that arrive while those are busy wait for the next statement and share it, up to 500 per statement; with few enqueues, each is written at once. A batch request is one statement of its own.
- **The connection pool.** Enqueues, attempt results and the dispatcher share one pool, whose default size is the number of CPUs (minimum 4). Since single enqueues use at most 4 connections however many requests arrive, they leave the rest to the dispatcher and the API's reads. A larger pool (`pool_max_conns` in `WORKLY_DATABASE_URL`) helps when many other requests run at once, keeping in mind that every replica opens its own pool against Postgres's `max_connections`.
- **SQLite's single writer.** Enqueues and attempt results take turns on one connection. Deliveries saturate around 6,000 tasks/s and enqueues around 20,000 tasks/s, far beyond what `workly dev` and small installs need.
- **Global in-flight limit.** At most 100 deliveries run at once by default (`WORKLY_MAX_IN_FLIGHT`), so throughput to a slow endpoint is at most 100 ÷ response time (e.g. 200 tasks/s at 500 ms). Per-queue `max_concurrency` (3 by default) limits it further; the load test raises it so only the global limit applies. To an endpoint as fast as the load test's, raising the global limit doesn't help: at 4,000 tasks/s on SQLite, before enqueues shared statements, 400 slots delivered 2,856/s against 2,880/s with 100.

Bottlenecks found while load testing, and fixed:

- Wake-up notifications used to be sent inside the enqueue transaction. A Postgres transaction that sends `NOTIFY` holds a database-wide lock through its commit, so every enqueue waited for the previous one's WAL flush. Enqueues topped out at about 1,300/s. The server now sends the wake-up after commit, coalesced so that at most one is in flight at a time.
- The dispatcher claimed tasks with one `UPDATE` per task. Claiming happens in one loop, so round trips capped dispatch at about 1,100 tasks/s. It now claims each batch with one statement.
- Each delivery recorded its result in a transaction of its own, so Postgres connections queued on the write-ahead log, one commit per delivery. Results that finish while a write is in flight now share the next one: one transaction and two statements per batch. On the same machine, saturation went from about 1,460 to 1,840 tasks/s with the default pool, from about 1,780 to 2,260–2,670 with 40 connections, and from about 2,090 to 3,180 on SQLite.
- An enqueue took five round trips: a transaction around making sure the queue exists, the insert, and reading the task back. It is now one statement, which inserts the task if its queue exists and returns it; the queue is created only when that finds none. Claiming a batch went from four round trips to one the same way. On Postgres, saturation went from about 1,740 to 2,830 tasks/s with the default pool, and delivery latency at 2,000/s from seconds to milliseconds.
- The batch endpoint inserted its tasks one statement at a time, so 100 tasks took about 100 round trips in one transaction. It now inserts them with one statement; tasks that statement can't insert (an idempotency key already taken, or a queue that doesn't exist yet) go through the single-task path. With 64 batches of 100 at once, enqueues went from 12,130 to 25,480 tasks/s on Postgres and from 6,840 to 19,150 on SQLite.
- Each single enqueue was a statement and a commit of its own, so concurrent enqueues queued for connections and, on SQLite, for its one writer. Enqueues that arrive while others are being written now share the next statement. With 64 requests at once, enqueues went from about 5,500 to 15,000 tasks/s on Postgres and from 4,100 to 19,700 on SQLite. SQLite's delivery limit went from about 2,900 to 6,000 tasks/s, as enqueues take far fewer turns on its connection.

## Reproducing

Start a server, then run the load test against it:

```sh
just dev                          # SQLite on :7337
just loadtest -rate 1000 -duration 20s
just loadtest -paused -rate 0 -batch 100 -workers 16 -duration 20s   # enqueues only
```

`-paused` keeps the queue paused so nothing is delivered, `-rate 0` sends requests as fast as the workers can, and `-batch` sets the tasks per request.

For Postgres, run `just serve`, or `workly serve` with `WORKLY_DATABASE_URL` and `WORKLY_SIGNING_SECRET` set. The load test recreates its queue (`loadtest`) on each run. See `go run ./tools/loadtest -h` for options.
