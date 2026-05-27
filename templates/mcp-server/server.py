"""Hello-MCP server template.

A minimal HTTP server that speaks a tiny subset of the Model Context
Protocol over JSON-RPC. The platform's gateway routes
``https://<agent>.<tenant>.<domain>`` to this process.

Endpoints:
  GET  /healthz      → 200 OK (used by the AgentDeployment readiness probe)
  POST /mcp          → JSON-RPC envelope; v1 implements ``initialize``,
                       ``tools/list`` and a single ``tools/call`` named ``echo``.

Replace ``echo`` and ``tools/list`` with your real tools. The platform
makes no other assumptions about your server.
"""

from __future__ import annotations

import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


PORT = int(os.environ.get("PORT", "8080"))


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def handle_rpc(req: dict) -> dict:
    method = req.get("method", "")
    req_id = req.get("id")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "serverInfo": {"name": "hello-mcp", "version": "0.1.0"},
                "capabilities": {"tools": {}},
            },
        }

    if method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": [
                    {
                        "name": "echo",
                        "description": "Echo back the input.",
                        "inputSchema": {
                            "type": "object",
                            "required": ["text"],
                            "properties": {"text": {"type": "string"}},
                        },
                    }
                ]
            },
        }

    if method == "tools/call":
        params = req.get("params") or {}
        if params.get("name") == "echo":
            text = (params.get("arguments") or {}).get("text", "")
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {"content": [{"type": "text", "text": text}]},
            }

    return {
        "jsonrpc": "2.0",
        "id": req_id,
        "error": {"code": -32601, "message": f"method not found: {method}"},
    }


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/healthz":
            self.send_response(200)
            self.send_header("content-type", "text/plain")
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/mcp":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("content-length") or "0")
        raw = self.rfile.read(length) if length > 0 else b""
        try:
            req = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b"bad json")
            return
        resp = handle_rpc(req)
        body = json.dumps(resp).encode("utf-8")
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args: object) -> None:  # silence default stderr noise
        log(fmt % args)


def main() -> None:
    log(f"mcp-server listening on :{PORT}")
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
