# Performance

Workly's target is to sustain **1,000 tasks/s** end to end on one server, with deliveries starting within milliseconds of the enqueue. Both databases meet it with room to spare.

## Results

Each run enqueues tasks at a fixed rate for 20 s, one API request per task (as a typical client does, with 64 concurrent requests), to a no-op endpoint that answers `204`. Latency is measured from just before the enqueue request to the moment the endpoint receives the delivery. Numbers are steady state, after a warm-up run.

**SQLite** (`workly dev`):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 2,000/s | 0.5 ms / 7.4 ms | 1.6 ms / 4.2 ms / 40 ms | 1,999/s, all |
| 3,000/s | 0.9 ms / 32 ms | 3.4 ms / 143 ms / 383 ms | 2,969/s, all |
| 4,000/s | 8.7 ms / 70 ms | backlog grows | ~3,180/s (saturated) |

**Postgres** (`workly serve`, default connection pool):

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 1,000/s | 2.6 ms / 37 ms | 5.9 ms / 14 ms / 125 ms | 999/s, all |
| 2,000/s | 5.5 ms / 69 ms | 455 ms / 2.4 s / 2.7 s | 1,999/s, all (at the limit) |
| 3,000/s | 22 ms / 64 ms | backlog grows | ~1,840/s (saturated) |

**Postgres with `pool_max_conns=40`:**

| Rate | Enqueue p50 / p99 | Enqueue → delivery p50 / p95 / p99 | Delivered |
|---|---|---|---|
| 2,000/s | 5.4 ms / 38 ms | 16 ms / 167 ms / 370 ms | 1,999/s, all |
| 3,000/s | 13 ms / 53 ms | backlog grows | 2,260–2,670/s (saturated) |

No run lost or duplicated a task.

Test setup: Apple M1 Pro (10 cores, 16 GB), macOS. Postgres 17 runs in Docker Desktop on the same machine, so every query crosses the Docker VM network (roughly 0.5 ms per round trip). Workly, the load generator and the endpoint all share the machine. Workly logs every attempt at info level, as it does by default. Dedicated Postgres on Linux should do better; treat these numbers as a floor.

## What limits throughput

- **Enqueue transactions.** Each enqueue request commits its own transaction (and makes sure its queue exists), so at high rates Postgres spends its time on those commits. Enqueues, attempt results and the dispatcher share one connection pool, whose default size is the number of CPUs (minimum 4); past about 2,000 tasks/s it is the bottleneck. Raise it with `pool_max_conns` in `WORKLY_DATABASE_URL`, keeping in mind that every replica opens its own pool against Postgres's `max_connections`.
- **SQLite's single writer.** Enqueues and attempt results take turns on one connection. It saturates around 3,200 tasks/s, far beyond what `workly dev` and small installs need.
- **Global in-flight limit.** At most 100 deliveries run at once, so throughput to a slow endpoint is at most 100 ÷ response time (e.g. 200 tasks/s at 500 ms). Per-queue `max_concurrency` (3 by default) limits it further; the load test raises it so only the global limit applies.

Bottlenecks found while load testing, and fixed:

- Wake-up notifications used to be sent inside the enqueue transaction. A Postgres transaction that sends `NOTIFY` holds a database-wide lock through its commit, so every enqueue waited for the previous one's WAL flush. Enqueues topped out at about 1,300/s. The server now sends the wake-up after commit, coalesced so that at most one is in flight at a time.
- The dispatcher claimed tasks with one `UPDATE` per task. Claiming happens in one loop, so round trips capped dispatch at about 1,100 tasks/s. It now claims each batch with one statement.
- Each delivery recorded its result in a transaction of its own, so Postgres connections queued on the write-ahead log, one commit per delivery. Results that finish while a write is in flight now share the next one: one transaction and two statements per batch. On the same machine, saturation went from about 1,460 to 1,840 tasks/s with the default pool, from about 1,780 to 2,260–2,670 with 40 connections, and from about 2,090 to 3,180 on SQLite.

## Reproducing

Start a server, then run the load test against it:

```sh
just dev                          # SQLite on :7337
just loadtest -rate 1000 -duration 20s
```

For Postgres, run `just serve`, or `workly serve` with `WORKLY_DATABASE_URL` and `WORKLY_SIGNING_SECRET` set. The load test recreates its queue (`loadtest`) on each run. See `go run ./tools/loadtest -h` for options.
