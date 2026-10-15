# Security

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: on the [Security tab](https://github.com/worklydev/workly/security), choose **Report a vulnerability**. Don't open a public issue.

Include what's affected (server, dashboard, an SDK), the version, and how to reproduce it. We'll acknowledge the report, keep you updated while we work on a fix, and credit you in the advisory unless you'd rather not be named.

## Supported versions

Workly is in early development. Fixes go into the latest release only.

## What counts

Anything that breaks the guarantees Workly makes, for example:

- reaching `/v1`, `/mcp` or `/metrics` on the main port without the API key when `WORKLY_API_KEY` is set;
- forging a delivery signature, or getting one accepted after its timestamp tolerance, with the SDKs' `verify_signature` / `verifySignature`;
- `read-only` MCP tools that change anything;
- one workspace reading or changing another's data, where a server hosts several.

Not a vulnerability in itself: a server run without `WORKLY_API_KEY` is open by design (it logs a warning at startup), and `workly dev` never requires auth. Workly delivers tasks to whatever URL they name, so keep a server that untrusted users can enqueue to away from internal services.
