# Configuration as code

Keep your queues and schedules in a `workly.yaml` next to your code, and deploy them with one command:

```sh
workly deploy
```

`workly deploy` compares the file with the server, shows what it will change, and creates and updates queues and schedules until the server matches. Running it again changes nothing, so it can run on every deploy of your app, and it replaces scripts that call the API to set things up.

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
| `workly plan` | Shows what `deploy` would create and update, setting by setting. Changes nothing. |
| `workly deploy` | Shows the plan, asks, and applies it. `--yes` (`-y`) skips the question, as CI needs; without a terminal to ask on, it's required. |

All three read `workly.yaml` in the current directory, or the file given with `--file` (`-f`). `plan` and `deploy` connect like the other commands: `--url` or `WORKLY_URL`, and `--api-key` or `WORKLY_API_KEY`. They exit with a non-zero status on an invalid file or a failed change; a deploy that fails partway reports what it had applied, and running it again finishes the job.

## The file

| Key | |
|---|---|
| `version` | Required. `1`. |
| `queues` | Queue names to their settings. |
| `schedules` | Schedule names to their settings. |

A queue or schedule's settings are the fields of its `PUT` request in the [API](../api/openapi.yaml), with the same names, defaults and limits: for queues `max_attempts`, `min_backoff_seconds`, `max_backoff_seconds`, `backoff_multiplier`, `timeout_seconds`, `retry_on_4xx`, `rate_per_second` and `max_concurrency`; for schedules `cron` and `url` (required), `timezone`, `method`, `headers`, `payload` or `body` and `content_type`, the retry settings, `overlap` and `missed` (see [Schedules](schedules.md)). A `payload` can be any YAML, and is sent as JSON.

Settings work as `PUT` does: **a setting left out of the file gets its default**, not whatever it was before. So the file describes each queue and schedule completely, and a change made in the dashboard to something the file declares is undone by the next deploy. To change it for good, change the file.

### Environment variables

`${NAME}` in a value is replaced with the environment variable `NAME`, so one file can serve several environments and keep secrets out of the repository. A variable that isn't set is an error, not an empty string. An unquoted value is read again after the replacement, so `max_concurrency: ${CONCURRENCY}` is a number.

The plan never shows header values or bodies, only that they changed, since they often carry secrets.

## What deploying changes, and what it doesn't

- It **creates** queues and schedules the file declares that the server doesn't have, and **updates** those whose settings differ. One whose settings already match isn't touched, so its `updated_at` stays put.
- It **never deletes** anything. A queue or schedule the file doesn't declare is left alone; the plan lists the schedules on the server that aren't in the file, so you notice the ones you meant to remove. Remove those with `workly schedules rm NAME`. Queues aren't listed: enqueueing a task creates its queue, so most queues have never been in a file.
- It **never pauses or resumes**. Pausing is something you do during an incident, and a deploy shouldn't undo it: a paused queue or schedule stays paused, and the plan says so.
- A section left out of the file isn't managed by it: a file without `schedules` doesn't look at schedules at all. An empty one (`schedules: {}`) declares none, which only changes what the plan lists.

## In CI

Deploy the file with your app. With GitHub Actions:

```yaml
- name: Deploy Workly configuration
  run: |
    curl -fsSL https://raw.githubusercontent.com/worklydev/workly/main/install.sh | sh
    workly deploy --yes
  env:
    WORKLY_URL: https://api.workly.run
    WORKLY_API_KEY: ${{ secrets.WORKLY_API_KEY }}
    API_URL: https://api.example.com
    JOBS_TOKEN: ${{ secrets.JOBS_TOKEN }}
```

Run `workly validate` in pull requests to catch mistakes before they merge; it needs no server or API key, only the environment variables the file uses.

For environments that differ by more than a few values, keep a file per environment and pick one with `--file`:

```sh
workly deploy --file workly.staging.yaml --yes
```
