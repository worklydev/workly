---
name: add-workly
description: Add background jobs, retried webhooks, cron schedules or multi-step workflows to an app with Workly, and test the handler end to end. Use when the user wants to move work out of a request, call an endpoint on a schedule, or chain HTTP steps with retries, using Workly.
---

# Add Workly to an app

Workly calls the app's own HTTP endpoints: a task is an HTTP request in a named queue, retried with backoff until the endpoint answers 2xx. There are no workers to run. The app needs two things: code that enqueues tasks (or a schedule or workflow), and an endpoint that handles them.

## 1. Read the docs for this server

Call `get_docs` before writing code, and read the pages for the job:

- `quickstart` and `concepts`: queues, tasks, attempts, retries and signing.
- `sdks/python` or `sdks/typescript`: the SDK for the app's language. For another language, the HTTP API works the same way.
- `schedules`, `workflows`, `groups` or `callbacks`, when the job needs them.
- `deploy`: keeping queues, schedules and workflows in a `workly.yaml` next to the code.

Use what the docs say for method names and settings; don't guess them.

## 2. Write the handler

- Make it an ordinary POST endpoint in the app. It answers 2xx when the work is done; anything else, or a timeout, makes Workly retry.
- Verify the signature against the raw request body before parsing it (`verify_signature` in Python, `verifySignature` in TypeScript), with the workspace's signing secret from the environment, never in code.
- Make it safe to run twice. Delivery is at least once, so a task can arrive again after a crash or a timeout. `Workly-Task-Id` is the same on every attempt; use it to skip work already done.
- Answer 4xx only for requests that can never succeed: a 4xx other than 408 or 429 fails the task at once, unless the queue sets `retry_on_4xx`.

## 3. Enqueue from the app

Enqueue with the SDK where the work comes from, with the API key from the environment (`WORKLY_API_KEY`). Pick queue settings for the endpoint: `max_concurrency` and `rate_per_second` for what it and its dependencies can take, `timeout_seconds` for how long a call may run, and `max_attempts` with the backoff settings. Put them in `workly.yaml` so they're reviewed with the code.

## 4. Test it end to end

1. Expose the handler at a URL Workly can reach. Hosted Workly can't reach `localhost`; use a deployed preview or a tunnel, or test locally against `workly dev`.
2. `enqueue_task` to the handler's URL with a sample payload, then `wait_for_task` to see how the first attempt went.
3. If it failed, `get_task` shows the status code and the start of the response body. Fix the handler and `retry_task`.
4. For a schedule, `update_schedule` creates it, `trigger_schedule` runs it now and `wait_for_run` shows the result. For a workflow, `trigger_workflow`, then `get_workflow_run`.

Ask before creating queues, schedules or workflows in a workspace the user hasn't said is for testing, since schedules keep calling their URL until they're paused or deleted.
