# MCP: Workly for coding agents

Every Workly server serves an [MCP](https://modelcontextprotocol.io) endpoint at `/mcp`, so coding agents (Claude Code, Cursor, VS Code, Codex and others) can see inside your queues and act on them. Connect one to `workly dev` while you write a task handler, and another to production while you debug:

> "Why are tasks in the `invoices` queue failing in prod?"
>
> "Enqueue a test task to my local `/tasks/send-email` handler and tell me how it answered."
>
> "I fixed the handler. Retry the failed tasks and check the first one succeeds."

There is nothing to install: the endpoint is part of the server, so its tools always match the server's version. It speaks the Streamable HTTP transport.

## Connect

`workly dev` prints the command for Claude Code when it starts:

```sh
claude mcp add --transport http workly http://localhost:7337/mcp
```

On [workly.run](https://workly.run), the endpoint is `https://api.workly.run/mcp`. Create an API key in the console (**API keys**) and send it as a bearer token; the agent sees that key's workspace:

```sh
claude mcp add --transport http workly-prod https://api.workly.run/mcp \
  --header "Authorization: Bearer $WORKLY_API_KEY"
```

For your own server, use its address instead (`https://<your server>/mcp`) and its `WORKLY_API_KEY`.

To share the setup with your team, add it to the project's `.mcp.json` (`--scope project`) and let each person's environment provide the key:

```json
{
  "mcpServers": {
    "workly": { "type": "http", "url": "http://localhost:7337/mcp" },
    "workly-prod": {
      "type": "http",
      "url": "https://api.workly.run/mcp",
      "headers": { "Authorization": "Bearer ${WORKLY_PROD_API_KEY}" }
    }
  }
}
```

**Cursor** (`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "workly": { "url": "http://localhost:7337/mcp" }
  }
}
```

**VS Code** (`.vscode/mcp.json`), with the key prompted for once and stored securely:

```json
{
  "inputs": [{ "type": "promptString", "id": "workly-key", "description": "Workly API key", "password": true }],
  "servers": {
    "workly": { "type": "http", "url": "http://localhost:7337/mcp" },
    "workly-prod": {
      "type": "http",
      "url": "https://api.workly.run/mcp",
      "headers": { "Authorization": "Bearer ${input:workly-key}" }
    }
  }
}
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.workly]
url = "http://localhost:7337/mcp"

[mcp_servers.workly-prod]
url = "https://api.workly.run/mcp"
bearer_token_env_var = "WORKLY_PROD_API_KEY"
```

## Read-only by default

`WORKLY_MCP` sets which tools the server offers:

| Value | Tools | Default for |
|---|---|---|
| `read-only` | Read tools only | `workly serve` |
| `full` | Read and write tools | `workly dev` |
| `off` | None: `/mcp` answers 404 | |

So pointing an agent at production can't enqueue, retry or cancel anything unless whoever runs the server opts in with `WORKLY_MCP=full`. workly.run serves the full tools. The endpoint takes the same credentials as `/v1`: with `WORKLY_API_KEY` set, an agent needs the key. Anyone with the key can already do everything through the API, so `read-only` is a guard against an agent's mistakes, not an access control.

## Tools

Read tools (marked read-only, so clients can run them without asking):

| Tool | What it does |
|---|---|
| `list_queues` | All queues, with policy and current work: due, scheduled, running, failed, how far behind |
| `get_queue` | One queue, plus how many tasks succeeded, failed and were cancelled recently, and its failed tasks grouped by error |
| `list_tasks` | Task summaries, newest first, by queue, state, idempotency key, last error and creation time |
| `get_task` | A task with its attempts: outcomes, status codes, errors, response bodies, durations |
| `get_task_delivery` | The exact signed request for a task, with a curl command to replay it against a local handler |
| `wait_for_task` | Waits for the first attempt since the task was enqueued or retried, and returns how it went |
| `list_schedules` | All [schedules](schedules.md), with their next run and how the last one went |
| `get_schedule` | One schedule, its next five times and its recent runs, including skipped ones and why |
| `list_runs` | A schedule's runs, newest first, optionally by state |
| `get_run` | A run with its attempts |
| `get_run_delivery` | The exact signed request for a run, with a curl command |
| `wait_for_run` | Waits for the first attempt of a run, and returns how it went |

Write tools (`WORKLY_MCP=full`):

| Tool | What it does |
|---|---|
| `enqueue_task` | Enqueues a task (creating the queue if needed) |
| `retry_task` | Delivers a task again now; replays a finished task |
| `cancel_task` | Cancels a pending or running task |
| `retry_failed_tasks` | Retries every failed task in a queue, or only those with one error |
| `cancel_pending_tasks` | Cancels a queue's pending tasks; they stay in the history and can be retried |
| `pause_queue`, `resume_queue` | Stops and restarts a queue's deliveries |
| `update_queue_policy` | Changes some of a queue's settings, keeping the rest |
| `update_schedule` | Creates a schedule, or changes some of its settings, keeping the rest |
| `trigger_schedule` | Runs a schedule now |
| `pause_schedule`, `resume_schedule` | Stops and restarts a schedule |
| `retry_run`, `cancel_run` | Delivers a run again, or cancels it |

Deleting queues isn't offered: it can't be undone, so it stays in the dashboard, CLI and API.

## What agents see

Task payloads, headers, response bodies and error messages come from your endpoints and whatever enqueues tasks, so they reach the agent as data that could contain anything. The server tells agents to treat them as untrusted, and shortens them to keep an agent's context small: response bodies to 2 KB, payloads to 8 KB, and a task's attempts to the latest 20. The full data is always in the dashboard and the API.

Each tool call is logged (`mcp tool call`, with the tool name, duration and any error).
