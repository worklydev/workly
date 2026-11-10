# Contributing to Workly

Thanks for helping. Bug reports, docs fixes and pull requests are all welcome.

## Before you start

- **Bugs:** [open an issue](https://github.com/worklydev/workly/issues/new/choose) with the version (`workly version`), how you run it (`dev`, `serve` on SQLite or Postgres) and what happened.
- **Security problems:** don't open an issue. Report them privately as described in [SECURITY.md](SECURITY.md).
- **Features and larger changes:** open an issue first, so we can agree on the shape before you write the code. Small fixes can go straight to a pull request.

Workly keeps its scope narrow on purpose: an HTTP task queue and scheduler with good diagnostics. It isn't a workflow engine, so there are no DAGs, workflow DSLs, chains or nested groups, and it never runs your code. Proposals that move it in that direction will most likely be declined, however well they're built.

## Setup

You need Go (the version in `go.mod`), Node 22+, [just](https://github.com/casey/just), and Docker for the Postgres tests. [uv](https://docs.astral.sh/uv/) is needed only for the Python SDK.

```sh
just dev        # local server and dashboard on :7337
just test       # unit tests and the SQLite conformance suite
just test-all   # also the Postgres tests (starts a container)
just lint       # gofmt, go vet, and the dashboard, SDK and docs checks
```

[AGENTS.md](AGENTS.md) describes the layout and the rules in more detail; it's written for coding agents, but it's the best map of the code for people too.

## Making a change

- **The API is spec first.** Change `api/openapi.yaml`, run `just generate`, then implement it. Error codes and metric names are stable API.
- **Both databases.** Store changes are written once in `internal/store/sqlstore`, get conformance tests in `internal/store/storetest`, and pass `just test-all`. Migrations go into both `postgres/migrations` and `sqlite/migrations`, with the same version.
- **Both SDKs.** When the API changes, update the Python and TypeScript SDKs in the same pull request.
- **Docs.** User-facing changes update `docs/`. A new `WORKLY_*` setting goes in `docs/configuration.md`.
- **Dependencies.** Workly prefers the standard library. Ask in an issue before adding a module.
- **Style.** Idiomatic Go, `gofmt`, table-driven tests. Comments explain why, not what.

Keep a pull request to one change, and say in its description what it changes and how you checked it.

## Sign your commits (DCO)

Workly uses the [Developer Certificate of Origin](https://developercertificate.org/) instead of a contributor license agreement. By adding a `Signed-off-by` line to a commit, you certify that you wrote the change, or otherwise have the right to submit it under the project's license:

```text
Signed-off-by: Ada Lovelace <ada@example.com>
```

`git commit -s` adds it for you, using your `user.name` and `user.email`. The name and email must match the commit's author. A check on every pull request looks for the line. If you forgot it, sign the commits you already made with:

```sh
git rebase --signoff main
git push --force-with-lease
```

## License

Workly is licensed under the [Apache License 2.0](LICENSE), and contributions are accepted under the same license.
