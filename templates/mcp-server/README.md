# mcp-server template

A minimal MCP server scaffold. `server.py` speaks a tiny JSON-RPC subset:
`initialize`, `tools/list`, `tools/call` (with one tool, `echo`). Replace
`tools/list` and `tools/call` with your real tool surface.

## Run locally

```bash
pip install -e .
python server.py    # listens on :8080
curl -s http://localhost:8080/healthz   # → ok
curl -s http://localhost:8080/mcp -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Deploy on the platform

```bash
agentctl agents create -f uipath-agent.yaml --image ghcr.io/me/hello-mcp:0.1
agentctl deploy <agentId>
# the platform brings up Deployment + Service in tenant-<id> and the
# gateway routes https://<agent>.<tenantId>.<configured-domain> to it.
```

## Contract

- **mode**: `server` (this template) — the platform projects to a
  long-running `Deployment` + `Service`, routed by the gateway.
- **healthPath**: `/healthz` (declared in `uipath-agent.yaml`). Readiness
  probe fails until this returns 200.
- **secrets**: declare names in `uipath-agent.yaml`; they're EnvFrom-mounted
  into the pod.
