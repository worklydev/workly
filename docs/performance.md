# Performance

Workly's target is to sustain **1,000 tasks/s** end to end on one server, with deliveries starting within milliseconds of the enqueue. Both databases meet it with room to spare.

## Results

Each run enqueues tasks at a fixed rate for 20 s, one API request per task (as a typical client does, with 64 concurrent requests), to a no-op endpoint that answers `204`. Latency is measured from just before the enqueue request to the moment the endpoint receives the delivery. Numbers are steady state, after a warm-up run.

**SQLite** (`workly dev`):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 2,000/s | 0.6 ms / 6.6 ms | 2.0 ms / 4.3 ms / 43 ms | 1,999/s, all |
| 3,000/s | 1.9 ms / 37 ms | 7.9 ms / 379 ms / 530 ms | 2,998/s, all (at the limit) |
| 4,000/s | 8.4 ms / 61 ms | backlog grows | ~3,230/s (saturated) |

**Postgres** (`workly serve`, default connection pool):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 1,000/s | 1.1 ms / 11 ms | 2.7 ms / 7.0 ms / 31 ms | 999/s, all |
| 2,000/s | 1.4 ms / 8.8 ms | 4.1 ms / 9.2 ms / 23 ms | 1,999/s, all |
| 3,000/s | 2.0 ms / 21 ms | 6.3 ms / 110 ms / 181 ms | 2,994/s, all (at the limit) |
| 4,000/s | 3.2 ms / 35 ms | backlog grows | ~2,830/s (saturated) |

A larger pool (`pool_max_conns=40`) lowers enqueue latency at 4,000/s and above (p99 16 ms), but deliveries saturate at the same rate, about 2,900/s.

No run lost or duplicated a task.

Test setup: Apple M1 Pro (10 cores, 16 GB), macOS. Postgres 17 runs in Docker Desktop on the same machine, so every query crosses the Docker VM network (roughly 0.5 ms per round trip). Workly, the load generator and the endpoint all share the machine. Workly logs every attempt at info level, as it does by default. Dedicated Postgres on Linux should do better; treat these numbers as a floor.

## What limits throughput

- **The dispatch loop.** One loop claims tasks and one writer records results, each a statement or two per batch. On Postgres they saturate at about 2,900 deliveries/s here, whatever the pool size.
- **The connection pool.** Enqueues, attempt results and the dispatcher share one pool, whose default size is the number of CPUs (minimum 4). Each enqueue is one statement that commits on its own, so enqueues keep up well past the delivery rate; when they outrun it by far (5,000/s here) they crowd the dispatcher out of the pool and deliveries slow down. A larger pool (`pool_max_conns` in `WORKLY_DATABASE_URL`) softens that, keeping in mind that every replica opens its own pool against Postgres's `max_connections`.
- **SQLite's single writer.** Enqueues and attempt results take turns on one connection. It saturates around 3,200 tasks/s, far beyond what `workly dev` and small installs need.
- **Global in-flight limit.** At most 100 deliveries run at once, so throughput to a slow endpoint is at most 100 ÷ response time (e.g. 200 tasks/s at 500 ms). Per-queue `max_concurrency` (3 by default) limits it further; the load test raises it so only the global limit applies.

Bottlenecks found while load testing, and fixed:

- Wake-up notifications used to be sent inside the enqueue transaction. A Postgres transaction that sends `NOTIFY` holds a database-wide lock through its commit, so every enqueue waited for the previous one's WAL flush. Enqueues topped out at about 1,300/s. The server now sends the wake-up after commit, coalesced so that at most one is in flight at a time.
- The dispatcher claimed tasks with one `UPDATE` per task. Claiming happens in one loop, so round trips capped dispatch at about 1,100 tasks/s. It now claims each batch with one statement.
- Each delivery recorded its result in a transaction of its own, so Postgres connections queued on the write-ahead log, one commit per delivery. Results that finish while a write is in flight now share the next one: one transaction and two statements per batch. On the same machine, saturation went from about 1,460 to 1,840 tasks/s with the default pool, from about 1,780 to 2,260–2,670 with 40 connections, and from about 2,090 to 3,180 on SQLite.
- An enqueue took five round trips: a transaction around making sure the queue exists, the insert, and reading the task back. It is now one statement, which inserts the task if its queue exists and returns it; the queue is created only when that finds none. Claiming a batch went from four round trips to one the same way. On Postgres, saturation went from about 1,740 to 2,830 tasks/s with the default pool, and delivery latency at 2,000/s from seconds to milliseconds.

## Reproducing

Start a server, then run the load test against it:

```sh
just dev                          # SQLite on :7337
just loadtest -rate 1000 -duration 20s
```

For Postgres, run `just serve`, or `workly serve` with `WORKLY_DATABASE_URL` and `WORKLY_SIGNING_SECRET` set. The load test recreates its queue (`loadtest`) on each run. See `go run ./tools/loadtest -h` for options.
