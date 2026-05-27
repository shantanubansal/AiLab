# `ailab` — Python client

```bash
pip install -e .
```

```python
from ailab import Client

with Client("http://localhost:8080", token="dev:<tenantId>:<userId>") as cli:
    agent = cli.create_agent({
        "schemaVersion": "v1",
        "name": "hello",
        "mode": "job",
        "runtime": "container",
        "image": "hello-world",
    })
    run = cli.trigger_run(agent["id"], {"hi": "there"})

    for line in cli.stream_logs(run["id"]):
        print(line)

    print(cli.get_run(run["id"]))
```

Methods mirror the platform API exactly — see `pkg/sdk-go/client.go` and
`api/openapi.yaml` for the full surface. All methods raise `APIError` on
non-2xx; the streaming helper closes the connection when the generator is
garbage-collected.
