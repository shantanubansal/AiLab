"""HTTP client for the AiLab API.

The client is a thin wrapper over the v1 REST surface. All methods raise
APIError on non-2xx; streaming methods are generators that yield logs as
they arrive. We use httpx for both single requests and streaming since it
exposes a uniform sync API.
"""

from __future__ import annotations

from typing import Any, Iterator

import httpx


class APIError(RuntimeError):
    """Raised when the platform API returns a non-2xx status."""

    def __init__(self, status: int, body: str) -> None:
        self.status = status
        self.body = body
        super().__init__(f"api {status}: {body}")


class Client:
    """Synchronous platform client."""

    def __init__(
        self,
        base_url: str = "http://localhost:8080",
        *,
        token: str,
        timeout: float = 30.0,
    ) -> None:
        if not token:
            raise ValueError("token is required")
        self._http = httpx.Client(
            base_url=base_url.rstrip("/"),
            timeout=timeout,
            headers={"Authorization": f"Bearer {token}"},
        )
        self._token = token

    def close(self) -> None:
        self._http.close()

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

    # ---- internal ----
    def _request(self, method: str, path: str, **kwargs: Any) -> Any:
        resp = self._http.request(method, path, **kwargs)
        if resp.status_code >= 400:
            raise APIError(resp.status_code, resp.text.strip())
        if resp.status_code == 204 or not resp.content:
            return None
        return resp.json()

    # ---- Agents ----
    def list_agents(self) -> list[dict[str, Any]]:
        return self._request("GET", "/v1/agents")["agents"]

    def create_agent(self, manifest: dict[str, Any]) -> dict[str, Any]:
        return self._request("POST", "/v1/agents", json={"manifest": manifest})

    def get_agent(self, agent_id: str) -> dict[str, Any]:
        return self._request("GET", f"/v1/agents/{agent_id}")

    def delete_agent(self, agent_id: str) -> None:
        self._request("DELETE", f"/v1/agents/{agent_id}")

    # ---- Runs ----
    def list_runs(self, agent_id: str) -> list[dict[str, Any]]:
        return self._request("GET", f"/v1/agents/{agent_id}/runs")["runs"]

    def trigger_run(
        self, agent_id: str, inputs: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        return self._request(
            "POST", f"/v1/agents/{agent_id}/runs", json={"inputs": inputs or {}}
        )

    def get_run(self, run_id: str) -> dict[str, Any]:
        return self._request("GET", f"/v1/runs/{run_id}")

    def stream_logs(self, run_id: str) -> Iterator[str]:
        """Yield log lines as the api emits SSE frames.

        Closes the underlying stream on generator close. Use a for-loop or
        wrap in a try/finally to guarantee cleanup if you break early.
        """
        with self._http.stream("GET", f"/v1/runs/{run_id}/logs") as resp:
            if resp.status_code >= 400:
                raise APIError(resp.status_code, resp.text.strip())
            for line in resp.iter_lines():
                if line.startswith("data: "):
                    yield line[len("data: ") :]

    # ---- Triggers ----
    def list_triggers(self, agent_id: str) -> list[dict[str, Any]]:
        return self._request("GET", f"/v1/agents/{agent_id}/triggers")["triggers"]

    def create_webhook_trigger(self, agent_id: str, name: str) -> dict[str, Any]:
        """Create a webhook trigger. The plaintext secret is in the response under
        ``webhookSecret`` exactly once — store it now or it's unrecoverable."""
        return self._request(
            "POST",
            f"/v1/agents/{agent_id}/triggers",
            json={"kind": "webhook", "name": name},
        )

    def create_cron_trigger(
        self, agent_id: str, name: str, cron_expr: str
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/v1/agents/{agent_id}/triggers",
            json={"kind": "cron", "name": name, "cron": cron_expr},
        )

    # ---- Deploy ----
    def deploy(self, agent_id: str) -> None:
        self._request("POST", f"/v1/agents/{agent_id}/deploy")

    def undeploy(self, agent_id: str) -> None:
        self._request("DELETE", f"/v1/agents/{agent_id}/deploy")

    # ---- Builds ----
    def create_build(self, agent_id: str, source_url: str) -> dict[str, Any]:
        return self._request(
            "POST", f"/v1/agents/{agent_id}/builds", json={"sourceUrl": source_url}
        )
