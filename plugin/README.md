# Workly plugin

Connects your agent to [hosted Workly](https://workly.run), the HTTP task queue and scheduler. Workly delivers HTTP requests to your endpoints with retries, runs cron schedules and multi-step workflows, and records every delivery attempt: status code, error, the start of the response body and how long it took.

With the plugin, your agent can:

- See each queue's backlog, failures and latency, and each schedule's recent runs.
- Find out why tasks fail: failures grouped by error, each attempt's response, and the exact signed request with a curl command to replay it against a local handler.
- Retry, cancel or pause work once the handler is fixed, enqueue test tasks, and trigger schedules and workflows.
- Read Workly's docs, SDK and CLI references for the server it's connected to.

## What's in it

- **MCP server:** `https://api.workly.run/mcp`. You sign in to your Workly account in the browser when you connect, then choose a workspace and read-only or read and write access. A read-only connection can't change anything.
- **Skills:**
  - `add-workly`: add background jobs, retried webhooks, cron schedules or workflows to an app, and test the handler end to end.
  - `debug-failures`: find out why tasks, schedule runs or workflow runs fail, fix the handler, and retry what failed.

## Try it

- "Which of my Workly queues have failing tasks, and why?"
- "Show me how my Workly schedules ran in the last day."
- "Why did the last run of my workflow fail?"
- "Send a welcome email in the background after sign-up, with Workly."

## Requirements

A Workly account at [workly.run](https://workly.run). The Hobby plan is free. The plugin works with hosted Workly only. For a self-hosted server or `workly dev`, connect to its `/mcp` endpoint instead: see [MCP](https://workly.run/docs/mcp/).

## Links

- [Docs](https://workly.run/docs/mcp/)
- [Support](https://workly.run/support/)
- [Privacy policy](https://workly.run/privacy/)
- [Terms](https://workly.run/terms/)
