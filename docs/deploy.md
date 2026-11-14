# Configuration as code

Keep your queues, workflows and schedules in a `workly.yaml` next to your code, and deploy them with one command:

```sh
workly deploy
```

`workly deploy` compares the file with the server, shows what it will change, and creates and updates queues, workflows and schedules until the server matches. Running it again changes nothing, so it can run on every deploy of your app, and it replaces scripts that call the API to set things up.

## Example

A nightly job that refreshes stock prices (see [task groups](groups.md) for the job itself) and a morning refresh of financial data:

```yaml
version: 1

queues:
  stock-prices:
    max_concurrency: 10
    rate_per_second: 2      # the price API allows 120 calls a minute
    max_attempts: 3
  financial-data:
    max_concurrency: 5
    timeout_seconds: 120

schedules:
  daily-stock-prices:
    cron: "0 22 * * 1-5"
    timezone: Europe/Stockholm
    url: ${API_URL}/jobs/start-price-update
    headers:
      Authorization: Bearer ${JOBS_TOKEN}

  refresh-financial-data:
    cron: "0 6 * * *"
    timezone: Europe/Stockholm
    url: ${API_URL}/jobs/refresh-financial-data
    payload:
      full: true
```

Check it, see the plan, deploy it:

```console
$ workly validate
workly.yaml is valid: 2 queues, 2 schedules.

$ workly plan
Plan for workly.yaml on https://api.workly.run:

  + create queue stock-prices
  ~ update queue financial-data
      max_concurrency: 3 → 5
      timeout_seconds: 30 → 120
  + create schedule daily-stock-prices
  ~ update schedule refresh-financial-data
      cron: 0 5 * * * → 0 6 * * *
      headers.Authorization: changed

2 to create, 2 to update, 0 unchanged.

$ workly deploy
…
Apply these changes to https://api.workly.run? [y/N] y

Created queue stock-prices
Updated queue financial-data
Created schedule daily-stock-prices
Updated schedule refresh-financial-data

Deployed workly.yaml: 2 created, 2 updated.
```

## Commands

| Command | |
|---|---|
| `workly validate` | Checks the file without contacting the server: field names and types, cron expressions, timezones, URLs, headers, and that every `${NAME}` is set. It runs the server's own validation, so a file that passes is one the server accepts. |
| `workly plan` | Shows what `deploy` would create, update and (with [`prune`](#pruning)) delete, setting by setting, and which `${NAME:-default}` values took their default. Changes nothing. |
| `workly deploy` | Shows the plan, asks, and applies it. `--yes` (`-y`) skips the question, as CI needs; without a terminal to ask on, it's required. |

All three read `workly.yaml` in the current directory, or the file given with `--file` (`-f`). `plan` and `deploy` connect like the other commands: `--url` or `WORKLY_URL`, and `--api-key` or `WORKLY_API_KEY`. They exit with a non-zero status on an invalid file or a failed change; a deploy that fails partway reports what it had applied, and running it again finishes the job.

## The file

| Key | |
|---|---|
| `version` | Required. `1`. |
| `prune` | `true` to delete what the file leaves out. See [Pruning](#pruning). Default `false`. |
| `queues` | Queue names to their settings. |
| `workflows` | Workflow names to their `steps` (a list) and `max_duration_seconds`. |
| `schedules` | Schedule names to their settings. |
| `x-…` | Ignored, so a key such as `x-defaults` can hold [anchors](#sharing-settings) for the rest of the file. |

A queue or schedule's settings are the fields of its `PUT` request in the [API](../api/openapi.yaml), with the same names, defaults and limits: for queues `max_attempts`, `min_backoff_seconds`, `max_backoff_seconds`, `backoff_multiplier`, `timeout_seconds`, `retry_on_4xx`, `rate_per_second` and `max_concurrency`; for workflows `steps`, each with the settings in [Workflows](workflows.md#define-a-workflow), and `max_duration_seconds`; for schedules `cron` and `url` or `workflow` (required), `timezone`, `method`, `headers`, `payload` or `body` and `content_type`, the retry settings, `overlap` and `missed` (see [Schedules](schedules.md)). A `payload` can be any YAML, and is sent as JSON.

Settings work as `PUT` does: **a setting left out of the file gets its default**, not whatever it was before. So the file describes each queue and schedule completely, and a change made in the dashboard to something the file declares is undone by the next deploy. To change it for good, change the file.

### Environment variables

`${NAME}` in a value is replaced with the environment variable `NAME`, so one file can serve several environments and keep secrets out of the repository. A variable that isn't set is an error, not an empty string. An unquoted value is read again after the replacement, so `max_concurrency: ${CONCURRENCY}` is a number.

`${NAME:-default}` uses `default` when `NAME` is unset or empty, as in the shell, so one file can work locally without exporting anything:

```yaml
url: ${API_URL:-http://localhost:8000}/jobs/nightly
```

`validate`, `plan` and `deploy` list every default they used, so a variable CI forgot to set shows up in the plan instead of quietly sending production schedules to `localhost`:

```console
Not set, so their defaults were used:
  API_URL: "http://localhost:8000" (schedules.daily-stock-prices.url, schedules.refresh-financial-data.url)
```

Keep secrets as plain `${NAME}`, without a default, so a missing one stops the deploy.

The plan never shows header values or bodies, only that they changed, since they often carry secrets.

### Sharing settings

YAML anchors (`&name`), aliases (`*name`) and merge keys (`<<`) work, so queues and schedules can share settings. A key starting with `x-` at the top of the file is ignored, which makes it a place to keep them:

```yaml
version: 1

x-price-api: &price-api
  max_concurrency: 2
  rate_per_second: 1        # one API key, shared by these queues

queues:
  stock-prices: *price-api
  earnings-dates:
    <<: *price-api
    max_attempts: 10        # keys given here win over merged ones
```

## Locally

`workly dev --config workly.yaml` applies the file each time the dev server starts, as `deploy` would, so a fresh database starts with your queues' limits instead of the defaults, and there's no separate deploy step. Restart it to apply changes to the file. With `${NAME:-default}` for the URLs, the same file serves both.

## What deploying changes, and what it doesn't

- It **creates** queues, workflows and schedules the file declares that the server doesn't have, and **updates** those whose settings differ. Workflows go before schedules, so a schedule can start one declared in the same file. A workflow run already going keeps the definition it started with. One whose settings already match isn't touched, so its `updated_at` stays put.
- It **doesn't delete** anything unless the file sets [`prune: true`](#pruning). A queue or schedule the file doesn't declare is left alone; the plan lists the workflows and schedules on the server that aren't in the file, so you notice the ones you meant to remove. Remove those with `workly schedules rm NAME` or `workly workflows rm NAME`. Queues aren't listed: enqueueing a task creates its queue, so most queues have never been in a file.
- It **never pauses or resumes**. Pausing is something you do during an incident, and a deploy shouldn't undo it: a paused queue or schedule stays paused, and the plan says so.
- A section left out of the file isn't managed by it: a file without `schedules` doesn't look at schedules at all. An empty one (`schedules: {}`) declares none, which only changes what the plan lists, or with `prune`, deletes every schedule.

### Pruning

With `prune: true` at the top of the file, the file is the whole truth for each section it has: deploying also deletes the queues, workflows and schedules on the server that the section leaves out, whoever created them. The plan lists them:

```
Plan for workly.yaml on https://api.workly.run:

  - delete schedule old-report
  - delete workflow old-refresh
  - delete queue old-emails (and its 3 failed tasks)

0 to create, 0 to update, 3 to delete, 4 unchanged.
```

- A section left out of the file is still not managed: a file with `prune: true` but no `schedules` leaves every schedule alone.
- Deleting a schedule deletes its runs. Deleting a workflow keeps its runs, and those still going finish.
- Deleting a queue deletes its tasks, so a queue the file leaves out must have nothing left to deliver: if one has tasks pending, scheduled or running, the plan fails and names it, and nothing is deployed. Add it under `queues`, or let its tasks finish (or cancel them with `workly queues cancel-pending NAME`). Its failed tasks are deleted with it, and the plan says how many. Workly's own queues (`_workflows`, `_callbacks`) are never deleted.
- Since enqueueing creates a queue, every queue your app enqueues to must be in the file, or the next deploy fails while it has work and deletes it when it hasn't.
- Deletes happen after the creates and updates: schedules first, then workflows, then queues, so nothing left points at something gone.

In CI, `deploy --yes` deletes without asking, so a queue or schedule removed from the file in a pull request is gone once it merges. Run `workly plan` on pull requests to see the deletes in review.

## In CI

Deploy the file with your app. With GitHub Actions:

```yaml
- name: Deploy Workly configuration
  run: |
    curl -fsSL "https://raw.githubusercontent.com/worklydev/workly/$WORKLY_VERSION/install.sh" | sh
    workly deploy --yes
  env:
    WORKLY_VERSION: v0.4.2   # pin the CLI, and bump it like any other dependency
    WORKLY_URL: https://api.workly.run
    WORKLY_API_KEY: ${{ secrets.WORKLY_API_KEY }}
    API_URL: https://api.example.com
    JOBS_TOKEN: ${{ secrets.JOBS_TOKEN }}
```

Run it once the app it calls is live, not before: a new schedule or workflow step can be delivered as soon as it's deployed, and if its endpoint isn't out yet, those deliveries fail and retry. A step after your app's deploy step in the same job does this. If your host deploys on its own and reports the deployment to GitHub, as Vercel does, run it on the `deployment_status` event instead, once the production deployment has succeeded:

```yaml
on: deployment_status

jobs:
  workly:
    if: github.event.deployment_status.state == 'success' && github.event.deployment.environment == 'Production'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4   # the deployed commit
      - name: Deploy Workly configuration
        run: |
          curl -fsSL "https://raw.githubusercontent.com/worklydev/workly/$WORKLY_VERSION/install.sh" | sh
          workly deploy --yes
        env:
          WORKLY_VERSION: v0.4.2
          WORKLY_URL: https://api.workly.run
          WORKLY_API_KEY: ${{ secrets.WORKLY_API_KEY }}
```

The environment's name is whatever your host calls it (Vercel's is `Production`).

In a Python project, `workly-cli==0.4.2` in your dev or CI requirements installs the same pinned binary with `pip` (it's what `workly[cli]` pulls in).

Run `workly validate` in pull requests to catch mistakes before they merge; it needs no server or API key, only the environment variables the file uses.

For environments that differ by more than a few values, keep a file per environment and pick one with `--file`:

```sh
workly deploy --file workly.staging.yaml --yes
```
