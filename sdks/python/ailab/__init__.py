"""AiLab platform client.

Typical usage::

    from ailab import Client

    cli = Client("http://localhost:8080", token="dev:<tenantId>:<userId>")
    agent = cli.create_agent({
        "schemaVersion": "v1", "name": "hello", "mode": "job",
        "runtime": "container", "image": "hello-world",
    })
    run = cli.trigger_run(agent["id"], {"hi": "there"})
    for line in cli.stream_logs(run["id"]):
        print(line)
"""

from .client import APIError, Client

__all__ = ["APIError", "Client"]
__version__ = "0.1.0"
