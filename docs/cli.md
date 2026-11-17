# CLI reference

<!-- Generated from the commands' help by cmd/workly/clidoc_test.go: run go test ./cmd/workly -run TestCLIReference -update after changing a command. -->

The `workly` binary is the server and the CLI. Install it with `pip install "workly[cli]"`, or see the [quickstart](quickstart.md#install) for the other ways. `workly COMMAND --help` prints the same help as this page.

The commands that call the API (queues, tasks, groups, workflows, schedules, runs, plan and deploy) connect to `--url` or `WORKLY_URL`, `http://localhost:7337` by default, with `--api-key` or `WORKLY_API_KEY`. For hosted Workly, that is `https://api.workly.run` and a key from the console. They print a table, or the API's JSON response with `--json`.

## workly dev

Run a local development server (SQLite, no auth).

`--config` applies a workly.yaml once the server is listening, as workly deploy
would, so a fresh database starts with the file's queues, workflows and
schedules rather than default settings. It's applied each time the server
starts: restart it to apply changes to the file.

```text
workly dev [flags]
```

Flags:

```text
--config string         a workly.yaml to apply at startup, as workly deploy would
--database-url string   postgres:// or sqlite:// URL (env WORKLY_DATABASE_URL)
--port int              port to listen on (env WORKLY_PORT) (default 7337)
```

## workly serve

Run the Workly server (for self-hosting).

Requires WORKLY_DATABASE_URL (postgres://…) and WORKLY_SIGNING_SECRET. Other
settings: WORKLY_API_KEY, WORKLY_PORT, WORKLY_METRICS_PORT, WORKLY_RETENTION,
WORKLY_RUN_RETENTION, WORKLY_SHUTDOWN_TIMEOUT, WORKLY_SIGNING_SECRET_PREVIOUS, WORKLY_MCP and more; see
https://github.com/worklydev/workly/blob/main/docs/configuration.md

```text
workly serve [flags]
```

Flags:

```text
--database-url string   postgres:// or sqlite:// URL (env WORKLY_DATABASE_URL)
--port int              port to listen on (env WORKLY_PORT) (default 7337)
```

## workly migrate

Apply database migrations and exit.

```text
workly migrate [flags]
```

Flags:

```text
--database-url string   postgres:// or sqlite:// URL (env WORKLY_DATABASE_URL)
--port int              port to listen on (env WORKLY_PORT) (default 7337)
```

## workly healthcheck

Exit 0 if the local server is ready (for Docker HEALTHCHECK).

```text
workly healthcheck [flags]
```

Flags:

```text
--url string   server URL (default http://localhost:$WORKLY_PORT)
```

## workly queues

List, configure, pause and resume queues.

Also: `queue`, `q`.

Flags for every `workly queues` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly queues ls

List queues with their current work.

```text
workly queues ls
```

Also: `list`.

### workly queues get

Show a queue's configuration and current work.

```text
workly queues get NAME
```

### workly queues put

Create or update a queue.

Declarative, like the API: the queue ends up with exactly the settings given,
and settings you leave out are reset to their defaults. Pausing is separate
(see pause and resume).

```text
workly queues put NAME [flags]
```

Also: `create`, `update`.

Examples:

```sh
workly queues put emails --rate 50 --max-concurrency 10
workly queues put webhooks --max-attempts 5 --timeout 10s --retry-on-4xx
```

Flags:

```text
--backoff-multiplier float   how fast the wait between retries grows (default 2)
--max-attempts int           total delivery attempts before a task fails (default 5)
--max-backoff duration       longest wait before a retry (default 1h0m0s)
--max-concurrency int        most deliveries in flight at once (default 3)
--min-backoff duration       shortest wait before a retry (default 2s)
--rate float                 most deliveries started per second (default no limit)
--retry-on-4xx               retry 4xx responses (by default only 408 and 429 are retried)
--timeout duration           how long to wait for a response (default 30s)
```

### workly queues pause

Stop delivering a queue's tasks (enqueueing still works).

```text
workly queues pause NAME
```

### workly queues resume

Resume delivering a paused queue's tasks.

```text
workly queues resume NAME
```

### workly queues failures

Group a queue's failed tasks by error, most common first.

```text
workly queues failures NAME
```

### workly queues retry-failed

Retry every failed task in a queue, e.g. after an endpoint outage.

```text
workly queues retry-failed NAME [flags]
```

Examples:

```sh
workly queues retry-failed emails
workly queues retry-failed emails --error 'HTTP 503 Service Unavailable'
```

Flags:

```text
--error string   only tasks whose last error is exactly this (see queues failures)
```

### workly queues cancel-pending

Cancel a queue's pending tasks, including scheduled ones.

```text
workly queues cancel-pending NAME [flags]
```

Flags:

```text
-y, --yes   don't ask for confirmation
```

### workly queues delete

Delete a queue and all its tasks.

```text
workly queues delete NAME [flags]
```

Also: `rm`.

Flags:

```text
-y, --yes   don't ask for confirmation
```

## workly tasks

Enqueue, inspect, retry and cancel tasks.

Also: `task`, `t`.

Flags for every `workly tasks` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly tasks enqueue

Enqueue a task to deliver to URL, and print its ID.

The queue is created with default settings if it doesn't exist.
`--payload` and `--body` take a value, @file, or `-` for stdin.

```text
workly tasks enqueue QUEUE URL [flags]
```

Examples:

```sh
workly tasks enqueue emails https://api.example.com/send -d '{"user_id": 123}'
workly tasks enqueue reports https://api.example.com/build --delay 10m -k report-2026-09
cat event.json | workly tasks enqueue events https://api.example.com/ingest -d -
```

Flags:

```text
    --at string                deliver at this time (RFC 3339, e.g. 2026-10-01T09:00:00Z)
    --body string              raw body: a value, @file, or - for stdin
    --content-type string      content type of --body (default text/plain; charset=utf-8)
    --delay duration           deliver after this delay, e.g. 30s or 2h
    --group string             add the task to this open group (see workly groups)
-H, --header stringArray       header sent with every delivery, "Name: value" (repeatable)
-k, --idempotency-key string   don't enqueue if the queue already has a task with this key
    --max-attempts int         override the queue's max attempts
-X, --method string            HTTP method (default POST)
    --on-complete string       POST a summary of the task to this URL each time it finishes
-d, --payload string           JSON body: a value, @file, or - for stdin
    --timeout duration         override the queue's response timeout
```

### workly tasks get

Show a task and its delivery attempts.

```text
workly tasks get ID
```

### workly tasks ls

List tasks, newest first.

```text
workly tasks ls [flags]
```

Also: `list`.

Examples:

```sh
workly tasks ls --queue emails --state failed
workly tasks ls -k report-2026-09
```

Flags:

```text
    --cursor string            continue from a previous page
    --error string             only tasks whose last error is exactly this (see queues failures)
    --group string             only this group's tasks
-k, --idempotency-key string   only tasks with this idempotency key
    --limit int                tasks per page (at most 200) (default 20)
    --queue string             only this queue's tasks
    --state string             only tasks in this state: pending, running, succeeded, failed or cancelled
```

### workly tasks retry

Deliver a task now; replays a finished task with fresh attempts.

```text
workly tasks retry ID
```

### workly tasks cancel

Cancel a pending or running task.

```text
workly tasks cancel ID
```

## workly groups

A task group tracks a set of tasks and enqueues a callback task once it is
closed and all of them have finished. Enqueue into a group with
workly tasks enqueue `--group`.

Also: `group`, `g`.

Flags for every `workly groups` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly groups create

Create an open group and print its ID.

```text
workly groups create [flags]
```

Examples:

```sh
group=$(workly groups create --name daily-prices --callback-url https://api.example.com/refresh)
workly tasks enqueue prices https://api.example.com/price -d '{"symbol": "NET"}' --group $group
workly groups close $group
```

Flags:

```text
    --callback-url string      URL of the callback task, enqueued when the group completes
-H, --header stringArray       header sent with the callback, "Name: value" (repeatable)
-k, --idempotency-key string   return the existing group with this key instead of creating one
    --name string              a label, e.g. the job this group is one run of
```

### workly groups get

Show a group's status and progress.

```text
workly groups get ID
```

### workly groups ls

List groups, newest first.

```text
workly groups ls [flags]
```

Also: `list`.

Flags:

```text
--cursor string   continue from a previous page
--limit int       groups per page (at most 200) (default 20)
--name string     only groups with this name
--status string   only groups in this status: open, running, succeeded or failed
```

### workly groups close

Stop a group accepting tasks, so it completes once they finish.

```text
workly groups close ID
```

## workly workflows

A workflow runs HTTP steps in dependency order, each delivered as a task. A
step's endpoint can hand its completion to a task group by answering with
Workly-Wait-For: group/<id>. Define workflows in workly.yaml and apply them
with workly deploy.

Also: `workflow`, `wf`.

Flags for every `workly workflows` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly workflows ls

List workflows and how each last ran.

```text
workly workflows ls
```

Also: `list`.

### workly workflows get

Show a workflow's steps and their dependencies.

```text
workly workflows get NAME
```

### workly workflows rm

Delete a workflow's definition; its runs are kept.

```text
workly workflows rm NAME [flags]
```

Flags:

```text
-y, --yes   delete without asking
```

### workly workflows trigger

Start a run of a workflow and print its ID. `--input` is JSON passed to every
step without a body of its own; it takes a value, @file, or `-` for stdin.

```text
workly workflows trigger NAME [flags]
```

Also: `start`.

Examples:

```sh
workly workflows trigger daily-refresh --input '{"date": "2026-11-13"}'
run=$(workly workflows trigger daily-refresh -k 2026-11-13) && workly workflows run $run
```

Flags:

```text
-k, --idempotency-key string   return the existing run with this key instead of starting one
-i, --input string             JSON input for the run: a value, @file or - for stdin
```

### workly workflows runs

List workflow runs, newest first.

```text
workly workflows runs [NAME] [flags]
```

Flags:

```text
--cursor string   continue from a previous page
--limit int       runs per page (at most 200) (default 20)
--status string   only runs in this status: running, succeeded, failed or cancelled
```

### workly workflows run

Show a workflow run: each step, and why it is where it is.

```text
workly workflows run ID
```

### workly workflows cancel

Cancel a workflow run: steps not yet started are skipped.

```text
workly workflows cancel ID
```

### workly workflows retry

Reopen a failed or cancelled workflow run, under the definition it
started with and with the same input. Steps that succeeded stay; the rest
run again, each on its old task with a fresh set of attempts, and a step
that was waiting for a group follows the group.

```text
workly workflows retry ID
```

## workly schedules

Create, inspect, run and pause schedules.

A schedule calls a URL on a cron timetable. Each call is a run, with its own
attempts and retries. Schedules are separate from queues and tasks.

Also: `schedule`, `s`.

Flags for every `workly schedules` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly schedules ls

List schedules with their next and last run.

```text
workly schedules ls
```

Also: `list`.

### workly schedules get

Show a schedule's settings, next slots and recent runs.

```text
workly schedules get NAME
```

### workly schedules put

Create or update a schedule that calls URL, or with `--workflow` (and no
URL) starts a run of that workflow, with `--payload` as the run's input.

Declarative, like the API: the schedule ends up with exactly the settings
given, and settings you leave out are reset to their defaults. Pausing is
separate (see pause and resume). `--payload` and `--body` take a value, @file,
or `-` for stdin.

`--cron` takes five fields (minute hour day-of-month month day-of-week), or
@hourly, @daily, @weekly, @monthly or @yearly.

```text
workly schedules put NAME [URL] [flags]
```

Also: `create`, `update`.

Examples:

```sh
workly schedules put nightly-report https://api.example.com/reports/nightly \
    --cron '0 3 * * *' --timezone Europe/Stockholm -d '{"kind": "daily"}'
workly schedules put sync https://api.example.com/sync --cron '*/15 * * * mon-fri' --overlap allow
workly schedules put daily-refresh --workflow daily-refresh --cron '0 3 * * *'
```

Flags:

```text
    --backoff-multiplier float   how fast the wait between retries grows (default 2)
    --body string                raw body: a value, @file, or - for stdin
    --content-type string        content type of --body (default text/plain; charset=utf-8)
    --cron string                when to run, e.g. '0 3 * * *' or @hourly (required)
-H, --header stringArray         header sent with every run, "Name: value" (repeatable)
    --max-attempts int           total delivery attempts before a run fails (default 3)
    --max-backoff duration       longest wait before a retry (default 5m0s)
-X, --method string              HTTP method (default POST)
    --min-backoff duration       shortest wait before a retry (default 5s)
    --missed string              slots missed while the server was down: skip, or catch_up (run the latest once) (default "skip")
    --on-complete string         POST a summary of each run to this URL when it finishes
    --overlap string             a slot that comes while the previous run is still going: skip or allow (default "skip")
-d, --payload string             JSON body: a value, @file, or - for stdin
    --retry-on-4xx               retry 4xx responses (by default only 408 and 429 are retried)
    --timeout duration           how long to wait for a response (default 30s)
    --timezone string            IANA timezone the cron expression is read in, e.g. Europe/Stockholm (default "UTC")
    --workflow string            start a run of this workflow instead of calling a URL
```

### workly schedules pause

Stop a schedule from running on its timetable.

```text
workly schedules pause NAME
```

### workly schedules resume

Resume a paused schedule from its next slot.

```text
workly schedules resume NAME
```

### workly schedules trigger

Run a schedule now, outside its timetable, and print the run's ID.

The run is delivered right away, even if the schedule is paused or a run is in
flight. It doesn't change when the schedule next runs.

```text
workly schedules trigger NAME
```

Also: `run`.

### workly schedules delete

Delete a schedule and all its runs.

```text
workly schedules delete NAME [flags]
```

Also: `rm`.

Flags:

```text
-y, --yes   don't ask for confirmation
```

## workly runs

Inspect, retry and cancel schedule runs.

Also: `run`, `r`.

Flags for every `workly runs` command:

```text
--api-key string   API key (env WORKLY_API_KEY)
--json             print the API's JSON response
--url string       server URL (env WORKLY_URL, default http://localhost:7337)
```

### workly runs ls

List runs of every schedule, or of one, newest first.

```text
workly runs ls [flags]
```

Also: `list`.

Examples:

```sh
workly runs ls --state failed
workly runs ls --schedule nightly-report
```

Flags:

```text
--cursor string     continue from a previous page
--limit int         runs per page (at most 200) (default 20)
--schedule string   only this schedule's runs
--state string      only runs in this state: pending, running, succeeded, failed, cancelled or skipped
```

### workly runs get

Show a run and its delivery attempts.

```text
workly runs get ID
```

### workly runs retry

Deliver a run now; a finished or skipped run gets fresh attempts.

```text
workly runs retry ID
```

### workly runs cancel

Cancel a pending or running run.

```text
workly runs cancel ID
```

## workly validate

Check workly.yaml, without contacting the server: its fields, cron
expressions, timezones, URLs and headers, and that every ${NAME} it uses is
set in the environment. It lists the ${NAME:-default} references that took
their default.

```text
workly validate [flags]
```

Flags:

```text
-f, --file string   the configuration file (default "workly.yaml")
```

## workly plan

Show what workly deploy would change, without changing anything.

`--format` markdown writes the plan for a pull request comment or GitHub's
step summary ($GITHUB_STEP_SUMMARY). `--detailed-exitcode` exits 0 when
there's nothing to change, 2 when there are changes, and 3 when they
include deletes, so CI can ask for a closer look; errors exit 1.

```text
workly plan [flags]
```

Flags:

```text
    --api-key string      API key (env WORKLY_API_KEY)
    --detailed-exitcode   exit 2 when there are changes, 3 when they include deletes
-f, --file string         the configuration file (default "workly.yaml")
    --format string       text, or markdown for a pull request or GitHub's step summary (default "text")
    --url string          server URL (env WORKLY_URL, default http://localhost:7337)
```

## workly deploy

Create and update the queues, workflows and schedules in workly.yaml, so
the server matches it. Shows the plan and asks first; `--yes` skips the
question, as CI needs. Running it again changes nothing. It never pauses or
resumes anything, and leaves what the file doesn't declare alone, unless
the file sets prune: true: then it deletes the queues, workflows and
schedules a section of the file leaves out. Workflow runs already going
keep the definition they started with.

```text
workly deploy [flags]
```

Flags:

```text
    --api-key string   API key (env WORKLY_API_KEY)
-f, --file string      the configuration file (default "workly.yaml")
    --url string       server URL (env WORKLY_URL, default http://localhost:7337)
-y, --yes              apply without asking
```

## workly version

Print the version.

```text
workly version
```
