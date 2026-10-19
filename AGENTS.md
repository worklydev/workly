# AGENTS.md

Conventions for coding agents (and humans) working in this repo.

## What this is

Workly is an open-source HTTP task queue: enqueue → queue → HTTP delivery → retry → success/failure. Next to queues, schedules call an endpoint on a cron timetable; each call is a run with its own attempts. Schedules and runs are separate from queues and tasks (own tables, API, settings, metrics and dashboard section) and share only internals: HTTP delivery, signing, backoff. The same binary runs as `workly dev` (SQLite), self-hosted (`workly serve` + Postgres, typically a Kubernetes pod) and as the hosted service at workly.run.

Task groups (`docs/groups.md`) track a set of tasks and enqueue one callback task when they've all finished. A group's counts change in the same transaction as its tasks' states (`sqlstore/groups.go`: every path that changes a task's state must update them), and it completes, enqueueing its callback, in the transaction that finishes its last task or closes it.

Keep the scope narrow. Workly is not a workflow engine: no DAGs, no workflow DSLs, no running user code. Groups stay one level deep: no dependencies between tasks, chains or nested groups.

## Commands

```sh
just dev        # local server on :7337 (SQLite in .workly/)
just test       # unit tests + SQLite conformance (Postgres tests skipped)
just test-all   # also runs Postgres tests (starts a container on :55432)
just test-e2e   # end-to-end tests: real `workly serve` replicas on Postgres (minutes)
just generate   # regenerate internal/api/gen.go from api/openapi.yaml
just lint       # gofmt + go vet + Prettier, type-check and lint for the dashboard and TypeScript SDK + docs site check + Python SDK ruff and pyright
just test-python  # Python SDK tests, including against a server built from this checkout
just test-typescript  # TypeScript SDK tests, likewise
just dashboard-dev  # dashboard with hot reload on :5173 (run `just dev` alongside)
just docs       # docs site with hot reload on :4321
just build      # binary with the dashboard embedded
just release-snapshot  # release binaries (dist/), images and workly-cli wheels (dist/pypi/), locally
just docker     # the Docker image for this machine, as workly:dev
```

Run `just test-all` before considering a change to `internal/store` done.

## Layout

- `cmd/workly/`: CLI entrypoint: the server (`dev`, `serve`, `migrate`, `healthcheck`) and API commands (`queues`, `tasks`, `groups`, `schedules`, `runs`) built on `internal/client`. API commands print human output by default and the API's JSON with `--json`; `tasks enqueue` prints only the ID, for scripts.
- `internal/client/`: Go API client generated from the spec (`just generate`), used by the CLI. Not public API.
- `server/`: the only public Go package. The private hosted repo (`worklydev/workly-cloud`) builds on it through extension points: store, workspace resolver, `Dashboard`, `WrapHandler`, `LoadConfig`, metrics, `DeliveryDialControl` (vetting delivery connections), `Admission` (limits on new tasks and schedules) and `Mailer` (sending alert emails). Keep it small and treat changes as API changes; workly-cloud builds this repo from a sibling checkout pinned to a commit, so check it still builds when you change them.
- `internal/api/`: HTTP layer. `gen.go` is generated; never edit it by hand. Handlers return `*apiError` for client errors; anything else becomes a 500.
- `internal/dispatch/`: the dispatcher (leader loop, HTTP delivery, batched result writes in `results.go`, retry policy, per-queue rate and concurrency limits, lease reaper, retention sweeper) and, in `schedules.go`, the schedule loop that runs beside it on the leader: firing due schedules (missed and overlap policies), delivering runs under its own in-flight cap, reaping and sweeping runs.
- `internal/alert/`: alerts about failed tasks and runs (`docs/alerts.md`): channels (email through a `Mailer`, Slack incoming webhooks), the message text, and the `Notifier` that runs on the dispatcher's leader, checking every minute and sending at most one alert per queue or schedule per cooldown.
- `internal/cron/`: cron expressions and next/previous times in a timezone, hand-written (no dependency). DST rules: a time in a skipped hour fires at the end of the gap, a repeated hour fires once.
- `internal/store/`: the `Store` interface; `sqlstore/` holds the SQL shared by both databases; `postgres/` and `sqlite/` add migrations and leader election; `migrate/` is the runner and `storetest/` the conformance suite.
- `internal/mcp/`: the MCP endpoint at `/mcp` (`docs/mcp.md`), hand-written: stateless Streamable HTTP, and tools that call the `/v1` handler in-process, so they share its validation, errors and workspace scoping. `WORKLY_MCP` picks `read-only` (default), `full` or `off`; tools with side effects are served only in `full`. When the API gains an operation agents need, add a tool and cover it in `server/mcp_test.go`.
- `internal/live/`: live dashboard updates. API writes (`markChanges`) and the dispatcher's state changes tell the `Hub` which workspace changed; it coalesces them into change notices for every replica through the store (`store.Changes`: Postgres `LISTEN`/`NOTIFY`, in-process on SQLite), and each replica passes them to the `GET /v1/events` streams it serves. Events carry no data: the dashboard refetches its live queries (`LIVE`, `lib/live.ts`) and polls slowly while the stream is up.
- `internal/metrics/`: Prometheus metrics on the standard library; metric names are stable API (`docs/metrics.md`).
- `internal/signing/`: delivery signatures. The scheme is shared with the SDKs, so changes are breaking; the SDK tests reuse the test vector in `signing_test.go`.
- `internal/id/`: prefixed ULIDs (`task_…`, `att_…`, `run_…`, `grp_…`).
- `internal/workspace/`: workspace resolution.
- `internal/config/`: `WORKLY_*` environment configuration.
- `dashboard/`: the web UI (React, Vite, Tailwind, TanStack Query), embedded into the binary from `dist/`. `src/api/` is generated from the spec. Design rules are in `dashboard/DESIGN.md`.
- `sdks/python/`: the Python SDK (`workly` on PyPI; uv, ruff, pyright strict). Hand-written and thin: sync and async clients share request building in `_requests.py`, and the only dependency is httpx. Its integration tests run against `workly dev` built from the same commit.
- `sdks/typescript/`: the TypeScript SDK (`@workly/sdk` on npm). No dependencies: `fetch` and Web Crypto only, so it runs on Node 22+, Bun, Deno and edge runtimes. Field names are camelCase, converted to and from the API's snake_case in `util.ts` (`fromAPI`, `toAPI`), except inside values that are the caller's own data: payloads, headers and per-queue counts. A new map-typed field in the spec (keys that are data, not names) goes in `verbatim` there. Timestamps stay RFC 3339 strings. Tests use `node:test` on the `.ts` sources directly; integration tests run against `workly dev` built from the same commit.
- `packaging/pypi/`: turns goreleaser's binaries into `workly-cli` platform wheels (stdlib-only `build_wheels.py`). `pip install "workly[cli]"` installs them; the SDK itself stays pure Python.
- `.goreleaser.yaml`, `install.sh`: release builds and the `curl | sh` installer.
- `deploy/kubernetes/`: example manifests (Deployment, Service, PDB), checked on a kind cluster by `release-check.yml`. A Helm chart is a later idea.
- `deploy/docker/`: the image's Dockerfile (distroless, non-root). It copies a prebuilt binary rather than compiling, so goreleaser and `just docker` build it from the same binaries as everything else.
- `api/openapi.yaml`: the API contract.
- `e2e/`: end-to-end tests against real `workly serve` processes and Postgres, behind the `e2e` build tag because they take minutes of wall-clock time; `go test ./...` skips them. `failover_test.go` SIGKILLs the leader around schedule slots and checks every slot gets exactly one run.
- `tools/loadtest/`: load generator; results in `docs/performance.md`.
- `docs/`: user-facing documentation, published at docs.workly.run and readable on GitHub: plain Markdown, one page per file, starting with a `# Title`; `README.md` is the home page. Link to other pages as `page.md` and to repo files by relative path (`../deploy/...`); the site rewrites both. A new `WORKLY_*` setting goes in `docs/configuration.md`. A new page also goes in the sidebar in `docs-site/astro.config.mjs`.
- `docs-site/`: the docs site (Astro and Starlight). `scripts/sync.mjs` copies `docs/` and `api/openapi.yaml` into it at build time; the API reference is rendered from the spec. `.github/workflows/docs.yml` publishes it on `vX.Y.Z` tags, so it describes the latest release.

## Releases

- **Server:** tag `vX.Y.Z` on main. `.github/workflows/release.yml` publishes binaries to GitHub releases, the `ghcr.io/worklydev/workly` image (tags `X.Y.Z`, `X.Y` and `latest`) and `workly-cli` wheels to PyPI, same version. `docs.yml` publishes docs.workly.run from the same tag.
- **Python SDK:** bump `version` in `sdks/python/pyproject.toml`, then tag `sdk-python/vX.Y.Z`. The tag must match.

- **TypeScript SDK:** bump `version` in `sdks/typescript/package.json` and `src/version.ts` (a test checks they match), then tag `sdk-typescript/vX.Y.Z`.

PyPI and npm uploads use trusted publishing from the `pypi` and `npm` GitHub environments; there are no tokens to rotate.

## Rules

1. **Spec first.** Change `api/openapi.yaml`, run `just generate`, then implement the generated `StrictServerInterface` method in `internal/api`. Only `/metrics`, `/mcp`, `/v1/events` (the dashboard's event stream, which the generated server can't serve) and `/_dev/echo` live outside the spec.
2. **Every store method that touches workspace data takes a `workspace.ID`.** No method may read or write across workspaces. The only exception is the dispatcher's methods (`store.Dispatch`), which find due work, sweep old tasks and report queue metrics across all workspaces. The workspace never appears in API paths; it is resolved from credentials by middleware and read with `workspace.FromContext`.
3. **Both stores, same behaviour.** Write SQL once in `sqlstore`, portable to both databases. Any new `Store` method needs conformance tests in `internal/store/storetest` and must pass on SQLite and Postgres. Add a migration file to *both* `postgres/migrations` and `sqlite/migrations` with the same version number. Never edit a migration that has been committed; add a new one.
4. **Timestamps are UTC.** Postgres uses `TIMESTAMPTZ`; SQLite stores Unix milliseconds as `INTEGER`. Stores return `time.Time` in UTC.
5. **Errors use one shape:** `{"error": {"code": "snake_case_code", "message": "..."}}`. Codes are stable API.
6. **Delivery is at-least-once.** Never write code that assumes exactly-once. A task whose outcome is unknown (crash, shutdown) is recorded as a `lost` attempt and delivered again.
7. **Keep dependencies few.** Prefer the standard library (`net/http`, `log/slog`); discuss before adding a module.
8. **Style:** idiomatic Go, `gofmt`, table-driven tests, `t.Context()` in tests. Comments explain *why*, not what.
9. **The dashboard is a library.** workly-cloud wraps `<Dashboard>` with its own auth, routes and nav, and compiles it as source. So: relative imports only (no `@/` alias), only the public `/v1` API, no hosted-only code or feature flags, and treat the props of `<Dashboard>` and the exports of `src/index.ts` as API. Follow `dashboard/DESIGN.md`.
10. **SDKs follow the API.** When the spec changes, update both SDKs (Python and TypeScript) in the same change: models ignore unknown fields, and `put` style methods send only the settings given. SDK methods map one-to-one to endpoints; no client-side workflow logic.
