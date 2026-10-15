# Self-hosting

In production, Workly is one binary (`workly serve`) plus Postgres. Run it as a container next to your apps; endpoints on a private network can be delivered to directly.

It needs two settings, a database and a signing secret; the rest is in [Configuration](configuration.md). Set `WORKLY_API_KEY` too, unless nothing untrusted can reach the server.

## Docker

The image is `ghcr.io/worklydev/workly` (linux/amd64 and arm64). It runs `workly serve` as a non-root user; pin a version tag (`X.Y.Z`) in production.

```sh
docker run -p 7337:7337 \
  -e WORKLY_DATABASE_URL=postgres://user:pass@db:5432/workly \
  -e WORKLY_SIGNING_SECRET=... \
  -e WORKLY_API_KEY=... \
  ghcr.io/worklydev/workly:0.1.0
```

To run Workly and Postgres together on one machine, use [`deploy/docker/compose.yaml`](../deploy/docker/compose.yaml):

```sh
export WORKLY_SIGNING_SECRET=$(openssl rand -hex 32) WORKLY_API_KEY=$(openssl rand -hex 32)
docker compose -f deploy/docker/compose.yaml up -d
```

The image has a health check (`workly healthcheck`, which probes `/readyz`), so Compose and Docker report when the server is ready.

## Kubernetes

[`deploy/kubernetes/workly.yaml`](../deploy/kubernetes/workly.yaml) is a starting point: a Deployment with two replicas, a Service and a PodDisruptionBudget. It reads its settings from a Secret:

```sh
kubectl create secret generic workly \
  --from-literal=database-url='postgres://user:pass@host:5432/workly?sslmode=require' \
  --from-literal=signing-secret="$(openssl rand -hex 32)" \
  --from-literal=api-key="$(openssl rand -hex 32)"
kubectl apply -f deploy/kubernetes/workly.yaml
```

What it sets up, and why:

- **Two replicas, one dispatcher.** Every replica serves the API and dashboard, but only the leader (chosen with a Postgres advisory lock) delivers tasks. If the leader's pod goes away, a standby takes over within seconds (as soon as Postgres sees its connection close); deliveries it had in flight are retried.
- **Probes.** Readiness is `/readyz` (the database is reachable and migrated), liveness `/healthz`.
- **Shutdown.** On `SIGTERM`, in-flight deliveries get `WORKLY_SHUTDOWN_TIMEOUT` (25s) to finish, within the default 30s `terminationGracePeriodSeconds`. If your queues have long timeouts, raise both. An interrupted delivery isn't lost: it's recorded as `lost` and retried.
- **Metrics.** `WORKLY_METRICS_PORT=9090` serves `/metrics` without the API key on a port the Service doesn't expose, and the pods carry `prometheus.io/*` scrape annotations. See [Metrics](metrics.md) for the metrics and example alerts.
- **Hardening.** Non-root, read-only root filesystem, no capabilities. Workly writes nothing to disk with Postgres.

To expose the dashboard outside the cluster, add an Ingress for the `workly` Service on port 7337, and keep `WORKLY_API_KEY` set: the dashboard asks for it. The same Ingress serves `/mcp`, so developers can point their coding agents at the cluster's Workly ([MCP](mcp.md)); it's read-only unless you set `WORKLY_MCP=full`.

Endpoints inside the cluster can be targeted directly, for example `http://billing.default.svc.cluster.local/tasks/invoice`.

## Upgrades

New versions migrate the database on startup; replicas starting together take turns. Check the release notes before upgrading across several versions. To migrate separately, for example from a CI job, set `WORKLY_AUTO_MIGRATE=false` and run `workly migrate` (with `WORKLY_DATABASE_URL` set) before rolling out. Until the database is migrated, new replicas report not ready.

## Backups

All state is in Postgres: back it up like any other database. Finished tasks are deleted after `WORKLY_RETENTION` (7 days), so the database stays about the size of your backlog plus a week of history.
