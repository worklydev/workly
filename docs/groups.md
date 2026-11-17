# Task groups

A task group tracks a set of tasks and calls you back once all of them have finished. Enqueue a task per item and get one callback with how they went: fan-out and fan-in, without writing the bookkeeping yourself.

```text
create group → enqueue tasks into it → close it → … tasks finish … → callback task
```

A group is one execution, not a reusable definition: a nightly job creates a new group each night, and can give them all the same `name`. A group has no dependencies between its tasks, no chains and no nested groups. To run steps in order with a group in the middle, use a [workflow](workflows.md): a step can hand its completion to a group with `Workly-Wait-For`, and the next step starts once the group has succeeded.

## Example: refresh stock prices, then rebuild the views

Every night, a [schedule](schedules.md) calls `/jobs/start-price-update`. That endpoint enqueues one task per company into a group, and the group's callback rebuilds the views that depend on the prices once every update has finished.

In Python, with Flask:

```python
from flask import Flask, abort, request
from workly import Callback, SignatureVerificationError, Workly, verify_signature

app = Flask(__name__)
workly = Workly()
API = "https://api.example.com"
SYMBOLS = ["NET", "DDOG", "SNOW", ...]  # the companies to update


def verified() -> dict:
    try:
        verify_signature(request.get_data(), request.headers)
    except SignatureVerificationError:
        abort(401)
    return request.get_json()


@app.post("/jobs/start-price-update")
def start_price_update():
    verified()
    day = request.headers["Workly-Scheduled-For"][:10]
    # Creates the group, enqueues the tasks into it in batches and closes it. The
    # idempotency key makes a retried run finish the same group rather than start another.
    workly.groups.enqueue(
        "stock-prices",
        ({"url": f"{API}/jobs/update-stock-price", "payload": {"symbol": s}} for s in SYMBOLS),
        name="daily-stock-prices",
        idempotency_key=f"stock-prices-{day}",
        on_complete=Callback(url=f"{API}/jobs/refresh-views"),
    )
    return "", 204


@app.post("/jobs/update-stock-price")
def update_stock_price():
    symbol = verified()["symbol"]
    ...  # fetch and store the price
    return "", 204


@app.post("/jobs/refresh-views")
def refresh_views():
    summary = verified()
    # {"group_id": "grp_…", "name": "daily-stock-prices", "status": "succeeded",
    #  "total": 500, "succeeded": 500, "failed": 0, "cancelled": 0}
    if summary["status"] == "succeeded":
        ...  # refresh the materialized views
    else:
        ...  # tell someone: summary["failed"] updates failed
    return "", 204
```

The same in TypeScript:

```ts
import { Workly } from '@worklydev/sdk';

const workly = new Workly();
const API = 'https://api.example.com';

export async function startPriceUpdate(day: string, symbols: string[]) {
  await workly.groups.enqueue(
    'stock-prices',
    symbols.map((symbol) => ({ url: `${API}/jobs/update-stock-price`, payload: { symbol } })),
    {
      name: 'daily-stock-prices',
      idempotencyKey: `stock-prices-${day}`,
      onComplete: { url: `${API}/jobs/refresh-views` },
    },
  );
}
```

`groups.enqueue` does three steps in one call: create the group, enqueue the tasks into it (in batches of 100, the most one request takes) and close it. With the group's `idempotency_key` it's safe to retry. The group is found again rather than created; a task without its own `idempotency_key` gets one from the group and its position, so none is counted twice; and a group that's already closed is returned as it is. It needs SDK 0.4.3 or later. With an older SDK, or over HTTP, do the three steps yourself:

```sh
curl -X POST localhost:7337/v1/groups -d '{
  "name": "daily-stock-prices",
  "on_complete": {"url": "https://api.example.com/jobs/refresh-views"}
}'
# {"id": "grp_01J9…", "status": "open", "total": 0, …}

curl -X POST localhost:7337/v1/queues/stock-prices/tasks -d '{
  "url": "https://api.example.com/jobs/update-stock-price",
  "payload": {"symbol": "NET"},
  "group_id": "grp_01J9…"
}'

curl -X POST localhost:7337/v1/groups/grp_01J9…/close
curl localhost:7337/v1/groups/grp_01J9…
```

Or from the CLI:

```sh
group=$(workly groups create --name daily-stock-prices \
  --callback-url https://api.example.com/jobs/refresh-views)
workly tasks enqueue stock-prices https://api.example.com/jobs/update-stock-price -d '{"symbol": "NET"}' --group $group
workly groups close $group
workly groups get $group
```

To try it locally, point the URLs at `http://localhost:7337/_dev/echo`.

## Lifecycle

| Status | |
|---|---|
| `open` | Accepting tasks. Its tasks run as soon as they're enqueued. |
| `running` | Closed, with tasks still pending or running. |
| `succeeded` | Every task succeeded. |
| `failed` | Every task finished, and at least one failed or was cancelled. |

- **Close the group once you've enqueued everything.** It can't complete while it's open, since more tasks may come, so you don't need to know the number of tasks up front. Closing a closed group does nothing, so it's safe to retry.
- A group whose tasks have all finished when it's closed completes at once, and so does a closed group with no tasks.
- Tasks in a group are ordinary tasks, with their queue's retries, limits and history. A task waiting to retry counts as remaining. A task that fails for good doesn't stop the others.
- `succeeded` and `failed` are final. Once a group has completed, nothing changes it.

## The callback

`on_complete` is a [callback](callbacks.md): a URL and optional headers, as for tasks and schedules. When the group completes, Workly enqueues a `POST` to the URL with the group's summary as its JSON body:

```json
{
  "group_id": "grp_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A",
  "name": "daily-stock-prices",
  "status": "failed",
  "total": 500,
  "succeeded": 498,
  "failed": 1,
  "cancelled": 1
}
```

It's enqueued exactly once: the group completes in the same database transaction that finishes its last task (or closes it), and that transaction enqueues the callback. A crash can't lose it, and two completions racing can't enqueue two.

From there it's an ordinary task in the [`_callbacks` queue](callbacks.md#the-_callbacks-queue): signed, retried and recorded, with the group's `callback_task_id` pointing to it. Delivery is at least once, so make the handler safe to run twice; `group_id` identifies the group. Headers in `on_complete` are sent with it.

The callback fires whether the group succeeded or failed: check `status`. A group without `on_complete` still completes, and you can poll it instead.

The body carries counts, not the tasks' results. To see which tasks failed, list them: `GET /v1/tasks?group_id=grp_…&state=failed`.

## Retries, cancellations and deleted queues

- **Retrying a task while its group is open or running** counts it as remaining again, so the group waits for the retry. That's how to fix a failure before the group completes.
- **Retrying a task after its group completed** delivers the task again but doesn't change the group. To redo the work as a group, create a new group.
- **Cancelling** a task counts it as cancelled, which makes the group `failed` once it completes.
- **Deleting a queue** takes its tasks out of their groups (`total` goes down), and a group may complete without them.

## Adding tasks safely

An enqueue checks that the group is open in the same transaction, so a task can't slip in after the group closes and be missed. Enqueueing into a closed or completed group fails with `group_closed` (409), and into a missing one with `group_not_found` (404). In a batch, all the tasks are enqueued or none.

Make it safe to retry (`groups.enqueue` in the SDKs does all of this):

- Give tasks an `idempotency_key`. A task enqueued again with the same key isn't counted twice, so you can resend a batch after a timeout.
- Give the group an `idempotency_key`. Creating it again returns the same group, so a retried request (or schedule run, as in the example) continues it rather than starting a second.

## Watching groups

The dashboard's **Groups** page lists groups with their progress. A group's page shows its counts, its callback and how it was delivered, and its tasks by state. `GET /v1/groups` lists groups newest first, filtered by `status` and `name`; `GET /v1/groups/{id}` returns one with `total`, `succeeded`, `failed`, `cancelled` and `remaining`. Agents get `list_groups` and `get_group` over [MCP](mcp.md), and `list_tasks` takes a `group_id`.

Completed groups are deleted after `WORKLY_RETENTION`, like finished tasks. Their tasks may go first; the group's counts stay accurate without them.
