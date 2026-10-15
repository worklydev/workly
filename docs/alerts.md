# Alerts

Workly can tell you when tasks or schedule runs fail, so you don't have to watch the dashboard. Alerts go to **channels**: email addresses and Slack incoming webhooks. Add them on the dashboard's Alerts page or through the API.

## What you get

A task or run counts as failed once it has used up its attempts, or got a response Workly doesn't retry. Retries that later succeed, and cancelled tasks, never alert.

- Within about a minute of a failure, every channel gets an alert. It names the queue or schedule, how many failed and the most common errors, and links to the failures in the dashboard.
- After that, a queue or schedule gets **at most one alert every 30 minutes**, covering everything that failed since its previous alert. A queue that keeps failing reminds you every half hour instead of once per task.
- Failures in several queues and schedules at once arrive as one alert.

Alerts cover failures from the moment the first channel is added; earlier ones aren't news.

## Slack

In Slack, create an [incoming webhook](https://api.slack.com/messaging/webhooks) for the channel alerts should go to, then add its URL:

```sh
curl -X POST localhost:7337/v1/alert-channels -d '{
  "type": "slack",
  "target": "https://hooks.slack.com/services/T000/B000/XXXX"
}'
```

The webhook URL is a secret: the API and the dashboard only show its end.

## Email

```sh
curl -X POST localhost:7337/v1/alert-channels -d '{"type": "email", "target": "oncall@example.com"}'
```

A self-hosted server sends email through your SMTP server, set with `WORKLY_SMTP_URL` and `WORKLY_SMTP_FROM` (see [Configuration](configuration.md)). Without them, `email_enabled` is `false` in `GET /v1/alert-channels` and adding an email channel fails with `email_unavailable`. Slack channels need no setup.

On workly.run, email alerts go to members of the workspace.

## Check a channel

Send a test alert to make sure a channel works:

```sh
curl -X POST localhost:7337/v1/alert-channels/ach_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A/test
```

If it can't be sent, the error says why (`alert_failed`), e.g. Slack's answer for a removed webhook.

From code:

```python
channel = client.alerts.create("slack", "https://hooks.slack.com/services/T000/B000/XXXX")
client.alerts.test(channel.id)
```

```ts
const channel = await workly.alerts.create('slack', 'https://hooks.slack.com/services/T000/B000/XXXX');
await workly.alerts.test(channel.id);
```

## Links

Alerts link to the dashboard when the server knows its address. Set `WORKLY_PUBLIC_URL` to where people open Workly, e.g. `https://workly.example.com`. `workly dev` links to `http://localhost:7337`.

## Delivery

Alerts are sent by the replica that delivers tasks, so a cluster sends each alert once. If every channel fails to take an alert (Slack is down, say), the next check, a minute later, tries again. A channel can get an alert twice if the server stops just after sending it.
