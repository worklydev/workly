# workly-cli

The [Workly](https://github.com/worklydev/workly) server and CLI, packaged for pip. Workly is an open-source HTTP task queue: enqueue a task, and Workly delivers it to your endpoint over HTTP, retrying with backoff until it succeeds.

```sh
pip install "workly[cli]"   # the Python SDK plus this package
workly dev                  # local server with a dashboard on http://localhost:7337
```

Or run it without installing anything:

```sh
uvx workly-cli dev
```

This package contains only the `workly` binary; the Python SDK is [`workly`](https://pypi.org/project/workly/). The binary is also available from [GitHub releases](https://github.com/worklydev/workly/releases).

```sh
workly dev                                  # local server (SQLite, no auth)
workly serve                                # production server (Postgres)
workly queues ls                            # queues and their backlog
workly tasks enqueue emails https://api.example.com/send -d '{"user_id": 123}'
workly tasks get task_01J9...               # a task and its delivery attempts
workly deploy --yes                         # apply workly.yaml: queues, workflows, schedules
```

In CI, pin it (`workly-cli==X.Y.Z`) to validate and deploy a [`workly.yaml`](https://workly.run/docs/deploy/) with the same version every time.

To start a server from Python, for example in a test fixture, `workly_cli.find_workly_bin()` returns the binary's path.
