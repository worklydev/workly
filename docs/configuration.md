# Configuration

The server is configured with environment variables. `--port` and `--database-url` flags override their variables.

| Variable | Default | Description |
|---|---|---|
| `WORKLY_DATABASE_URL` | `sqlite://.workly/dev.db` in `dev`; required for `serve` | `postgres://user:pass@host:5432/db` or `sqlite://path/to/file.db` (`sqlite:///abs/path.db` for an absolute path). |
| `WORKLY_SIGNING_SECRET` | `workly-dev-secret` in `dev`; required for `serve` | Signs every delivery (`Workly-Signature`). Generate one with `openssl rand -hex 32`. |
| `WORKLY_SIGNING_SECRET_PREVIOUS` | unset | During a secret rotation, also signs deliveries with the old secret. See [Rotating the signing secret](#rotating-the-signing-secret). |
| `WORKLY_API_KEY` | unset | If set, `/v1` and `/metrics` require `Authorization: Bearer <key>`. `serve` logs a warning when it's unset. Always ignored by `dev`. |
| `WORKLY_PORT` | `7337` | Port for the API and dashboard. |
| `WORKLY_METRICS_PORT` | unset | Serve `/metrics` on this port instead, without the API key. Keep it internal. |
| `WORKLY_RETENTION` | `168h` | How long succeeded, failed and cancelled tasks (with their attempts) are kept. `0` keeps them forever. |
| `WORKLY_RUN_RETENTION` | `WORKLY_RETENTION` | How long finished [schedule](schedules.md) runs are kept. Each schedule's latest run is always kept. `0` keeps them forever. |
| `WORKLY_SHUTDOWN_TIMEOUT` | `25s` | How long in-flight requests and deliveries get to finish on shutdown. Deliveries still running after it are retried. |
| `WORKLY_MAX_PAYLOAD_BYTES` | `1048576` | Largest task body, up to 100 MiB. |
| `WORKLY_AUTO_CREATE_QUEUES` | `true` | Enqueueing to a missing queue creates it with defaults. With `false`, it's a `404 queue_not_found`. |
| `WORKLY_AUTO_MIGRATE` | `true` | Apply database migrations on startup. With `false`, run `workly migrate` before starting new versions. |
| `WORKLY_MCP` | `read-only` (`full` in `dev`) | Tools served to coding agents at `/mcp`: `read-only`, `full` (adds enqueue, run now, retry, cancel, pause and settings changes) or `off`. See [MCP](mcp.md). |
| `WORKLY_PUBLIC_URL` | `http://localhost:7337` in `dev`; unset | Where people open Workly, e.g. `https://workly.example.com`. [Alerts](alerts.md) link to its dashboard; without it they have no links. |
| `WORKLY_SMTP_URL` | unset | SMTP server for email [alerts](alerts.md): `smtp://user:pass@host:587` (STARTTLS, required before the password is sent, except to localhost) or `smtps://user:pass@host:465`. Without it, alerts go to Slack only. |
| `WORKLY_SMTP_FROM` | unset | The address alerts are sent from, e.g. `Workly <alerts@example.com>`. Required with `WORKLY_SMTP_URL`. |
| `WORKLY_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `WORKLY_LOG_FORMAT` | `json` (`text` in `dev`) | `json` or `text`. |

Durations use Go's syntax: `90s`, `15m`, `168h`.

## Postgres

Workly needs Postgres 14 or newer. It creates its tables on startup (`WORKLY_AUTO_MIGRATE`); replicas starting together take turns.

Connection settings go in the URL:

- `sslmode=require` (or `verify-full`) for a managed database.
- `search_path=workly` keeps Workly's tables in their own schema. Create the schema first. Separate installs can share a database this way, each in its own schema.
- `pool_max_conns=20` raises the connection pool size (the default is 4, or the number of CPUs if higher). The dispatcher leader holds one more connection for its lock.

## The CLI

The `queues` and `tasks` commands talk to a server over its API:

| Variable | Flag | Default |
|---|---|---|
| `WORKLY_URL` | `--url` | `http://localhost:7337` |
| `WORKLY_API_KEY` | `--api-key` | unset |

The Python SDK reads the same two variables.

## Rotating the signing secret

Endpoints verify deliveries with the signing secret, so changing it takes three steps:

1. Set the new secret as `WORKLY_SIGNING_SECRET` and the old one as `WORKLY_SIGNING_SECRET_PREVIOUS`, and restart. Each delivery now carries both signatures, and endpoints still on the old secret keep verifying them.
2. Move your endpoints to the new secret.
3. Unset `WORKLY_SIGNING_SECRET_PREVIOUS` and restart.
