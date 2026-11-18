# MCP: Workly for coding agents

Every Workly server serves [MCP](https://modelcontextprotocol.io) over HTTP at `/mcp`, so there's nothing to install. Agents such as Claude Code, Codex, Cursor and VS Code use it to read queues, tasks and schedules, replay a delivery against a local handler, and retry what failed.

## Connect

Paste this into your agent and it connects itself:

```prompt wrap
Connect this app to Workly over MCP. Workly (https://workly.run) is an HTTP task queue and scheduler: it delivers HTTP requests to my endpoints with retries, runs cron schedules, and records every attempt.

1. Add Workly's MCP server (streamable HTTP) with this app's own way of adding a remote MCP server. Use the endpoint that fits this project, or ask me which:
   - Hosted Workly: https://api.workly.run/mcp. It signs in with OAuth in the browser; no API key.
   - `workly dev` on this machine: http://localhost:7337/mcp. No sign-in.
   - A self-hosted server: https://<server>/mcp, with the server's WORKLY_API_KEY as a bearer token. Read the key from an environment variable; don't write it into the config.
2. If it needs a sign-in, tell me how to start it in this app.
3. Once connected, call get_docs to learn how Workly works, then help me with what I'm building.

If this app can't connect to remote MCP servers, read https://workly.run/llms.txt and use Workly's HTTP API instead.
```

Or set it up yourself. How an agent signs in depends on where Workly runs:

| Server | Endpoint | Sign-in |
|---|---|---|
| [Hosted: workly.run](#hosted-worklyrun) | `https://api.workly.run/mcp` | OAuth in the browser, or an API key |
| [Local dev server: `workly dev`](#local-dev-server-workly-dev) | `http://localhost:7337/mcp` | None |
| [Self-hosted](#self-hosted) | `https://<your server>/mcp` | `WORKLY_API_KEY` as a bearer token |

### Hosted: workly.run

Add the server, then sign in. The console opens in your browser: sign in or create an account, pick a workspace, and choose read-only or read and write.

```sh title="Claude Code"
claude mcp add --transport http --scope user workly https://api.workly.run/mcp
claude mcp login workly
```

```sh title="Codex"
codex mcp add workly --url https://api.workly.run/mcp  # opens the sign-in
```

```jsonc title="Cursor"
// .cursor/mcp.json, or use the install link below
{
  "mcpServers": {
    "workly": { "url": "https://api.workly.run/mcp" }
  }
}
```

```jsonc title="VS Code"
// .vscode/mcp.json
{
  "servers": {
    "workly": { "type": "http", "url": "https://api.workly.run/mcp" }
  }
}
```

Cursor can also install it from a link: [Add to Cursor](cursor://anysphere.cursor-deeplink/mcp/install?name=workly&config=eyJ1cmwiOiJodHRwczovL2FwaS53b3JrbHkucnVuL21jcCJ9). For Claude or ChatGPT, see [chat apps](#claude-chatgpt-and-other-chat-apps).

For an agent that can't open a browser, such as one in CI, create an API key in the console (**API keys**) and send it as a bearer token, as for a [self-hosted](#self-hosted) server. The agent gets that key's workspace.

### Local dev server: `workly dev`

The dev server takes no key, and prints the Claude Code command when it starts.

```sh title="Claude Code"
claude mcp add --transport http workly-dev http://localhost:7337/mcp
```

```sh title="Codex"
codex mcp add workly-dev --url http://localhost:7337/mcp
```

```jsonc title="Cursor"
// .cursor/mcp.json
{
  "mcpServers": {
    "workly-dev": { "url": "http://localhost:7337/mcp" }
  }
}
```

```jsonc title="VS Code"
// .vscode/mcp.json
{
  "servers": {
    "workly-dev": { "type": "http", "url": "http://localhost:7337/mcp" }
  }
}
```

### Self-hosted

Send the server's `WORKLY_API_KEY` as a bearer token. Each config reads it from an environment variable (VS Code prompts for it once and stores it), so it can be committed and shared with your team.

```sh title="Claude Code"
claude mcp add --transport http --scope project workly-prod https://workly.example.com/mcp \
  --header 'Authorization: Bearer ${WORKLY_API_KEY}'
```

```sh title="Codex"
codex mcp add workly-prod --url https://workly.example.com/mcp --bearer-token-env-var WORKLY_API_KEY
```

```jsonc title="Cursor"
// .cursor/mcp.json
{
  "mcpServers": {
    "workly-prod": {
      "url": "https://workly.example.com/mcp",
      "headers": { "Authorization": "Bearer ${env:WORKLY_API_KEY}" }
    }
  }
}
```

```jsonc title="VS Code"
// .vscode/mcp.json
{
  "inputs": [{ "type": "promptString", "id": "workly-key", "description": "Workly API key", "password": true }],
  "servers": {
    "workly-prod": {
      "type": "http",
      "url": "https://workly.example.com/mcp",
      "headers": { "Authorization": "Bearer ${input:workly-key}" }
    }
  }
}
```

### Claude, ChatGPT and other chat apps

Chat apps that support remote MCP servers add workly.run as a custom connector: add one with the URL `https://api.workly.run/mcp`. Workly's console opens; sign in or create an account, pick a workspace, and choose read-only or read and write.

A chat app connects from its vendor's servers, not from your machine, so it can't reach `workly dev`. It reaches a self-hosted server only if the server has a public address and the app can send its API key as a bearer token.

## Read-only by default

`WORKLY_MCP` sets which tools the server offers:

| Value | Tools | Default for |
|---|---|---|
| `read-only` | Read tools only | `workly serve` |
| `full` | Read and write tools | `workly dev` |
| `off` | None: `/mcp` answers 404 | |

An agent pointed at production can't enqueue, retry or cancel anything unless whoever runs the server sets `WORKLY_MCP=full`. workly.run serves the full tools to API keys, and to OAuth connections given read and write access; a connection given read-only access gets the read tools. The endpoint takes the same credentials as `/v1`: with `WORKLY_API_KEY` set, an agent needs the key. Anyone with the key can already do everything through the API, so `read-only` is a guard against an agent's mistakes, not an access control.

## Tools

Tools return a record's fields with no value as `null` rather than leaving them out, so every task, run or schedule has the same keys.

Read tools (marked read-only, so clients can run them without asking):

| Tool | What it does |
|---|---|
| `list_queues` | All queues in summary: paused or not, rate and concurrency limits, current work (due, scheduled, running, failed, how far behind) and the last 24 hours (succeeded, failed, and p50 and p95 attempt duration with its sample size) |
| `get_queue` | One queue with its full policy, the last 24 hours, how many tasks succeeded, failed and were cancelled recently, and its failed tasks grouped by error |
| `list_tasks` | Task summaries, newest first, by queue, state, group, idempotency key, last error and creation time. Each says when its first attempt started and how long the latest took, so queue wait and handler time can be told apart |
| `get_task` | A task with its attempts (outcomes, status codes, errors, response bodies, durations), how many attempts it gets and how many are left |
| `get_task_delivery` | The exact signed request for a task, with a curl command to replay it against a local handler |
| `wait_for_task` | Waits for an attempt since the task was enqueued or retried, and returns how the latest went, with whether the task has finished or will be retried. Returns at once with the reason if its queue is paused or it isn't due before the wait would end |
| `list_groups` | [Task groups](groups.md), newest first, with their status and counts, by status and name |
| `get_group` | One group: its counts, its most recently failed tasks and its callback task |
| `list_schedules` | All [schedules](schedules.md) in summary: cron, timezone, URL, paused (and since when), next run (with the schedule's UTC offset) and how the last one went |
| `get_schedule` | One schedule, its next five times (with the schedule's UTC offset) and its recent runs, including skipped ones and why. A paused schedule's times are `next_runs_if_resumed` |
| `list_runs` | Runs of one schedule or of all of them, newest first, optionally by state |
| `get_run` | A run with its attempts |
| `get_run_delivery` | The exact signed request for a run, with a curl command |
| `wait_for_run` | Waits for an attempt of a run, and returns how the latest went, with whether the run has finished |
| `list_workflows` | All [workflows](workflows.md): their steps and how the last run went |
| `get_workflow` | One workflow's definition: steps, dependencies, queues |
| `list_workflow_runs` | Runs of one workflow or of all of them, newest first, optionally by status |
| `get_workflow_run` | A run with each step's status, task, last result and what it is blocked by or waiting for |
| `get_docs` | These docs, for the server's version, as Markdown: the list of pages, or one page, SDK and CLI references included |

Write tools (`WORKLY_MCP=full`):

| Tool | What it does |
|---|---|
| `enqueue_task` | Enqueues a task (creating the queue if needed), optionally into a group |
| `retry_task` | Delivers a task again now; replays a finished task |
| `cancel_task` | Cancels a pending or running task |
| `retry_failed_tasks` | Retries every failed task in a queue, or only those with one error |
| `cancel_pending_tasks` | Cancels a queue's pending tasks; they stay in the history and can be retried |
| `pause_queue`, `resume_queue` | Stops and restarts a queue's deliveries |
| `update_queue_policy` | Changes some of a queue's settings, keeping the rest |
| `delete_queue` | Deletes a queue and its finished tasks. Refuses while it has tasks due, scheduled or running, and for Workly's own queues |
| `update_schedule` | Creates a schedule, or changes some of its settings, keeping the rest |
| `trigger_schedule` | Runs a schedule now |
| `pause_schedule`, `resume_schedule` | Stops and restarts a schedule |
| `retry_run`, `cancel_run` | Delivers a run again, or cancels it |
| `delete_schedule` | Deletes a schedule and its runs |
| `trigger_workflow` | Starts a workflow run, with optional input and idempotency key |
| `cancel_workflow_run` | Cancels a workflow run: steps not yet started are skipped |
| `retry_workflow_run` | Reopens a failed or cancelled workflow run: its failed and skipped steps run again |
| `delete_workflow` | Deletes a workflow's definition; its runs are kept |

Write tools that change one task, run, queue or schedule return it as changed, so an agent can confirm the change without reading it again. The delete tools can't be undone, so they're marked destructive, and clients that confirm destructive tools ask first. `delete_queue` won't delete a queue with work left in it, so an agent can't lose tasks it didn't know about; run `cancel_pending_tasks` first if they should go.

Arguments are checked against each tool's schema before anything runs, and errors name the argument at fault (`limit must be an integer, not string`, `state must be one of …`), so an agent can correct its call.

## What agents see

Task payloads, headers, response bodies and error messages come from your endpoints and whatever enqueues tasks, so they could contain anything. The server tells agents to treat them as untrusted, and shortens them to keep an agent's context small: response bodies to 2 KB, payloads to 8 KB, and a task's attempts to the latest 20. The full data is always in the dashboard and the API.

Each tool call is logged (`mcp tool call`, with the tool name, duration and any error).
