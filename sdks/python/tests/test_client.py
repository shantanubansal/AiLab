"""Client tests using respx to stub httpx. Verifies bearer auth, JSON
encoding, error mapping, and SSE log frame parsing."""

import httpx
import pytest
import respx

from ailab import APIError, Client


@pytest.fixture()
def client():
    c = Client("http://api.test", token="dev:tenant:user")
    yield c
    c.close()


@respx.mock
def test_list_agents_sends_bearer(client: Client):
    route = respx.get("http://api.test/v1/agents").mock(
        return_value=httpx.Response(200, json={"agents": [{"id": "a1", "name": "x"}]})
    )
    assert client.list_agents() == [{"id": "a1", "name": "x"}]
    assert route.called
    assert route.calls.last.request.headers["authorization"] == "Bearer dev:tenant:user"


@respx.mock
def test_create_agent_body(client: Client):
    route = respx.post("http://api.test/v1/agents").mock(
        return_value=httpx.Response(201, json={"id": "a1"})
    )
    out = client.create_agent({"name": "x"})
    assert out == {"id": "a1"}
    assert route.calls.last.request.read() == b'{"manifest":{"name":"x"}}'


@respx.mock
def test_api_error_carries_status_and_body(client: Client):
    respx.get("http://api.test/v1/agents/missing").mock(
        return_value=httpx.Response(404, text="not found")
    )
    with pytest.raises(APIError) as exc:
        client.get_agent("missing")
    assert exc.value.status == 404
    assert "not found" in exc.value.body


@respx.mock
def test_stream_logs_parses_sse_frames(client: Client):
    body = b"data: line one\n\ndata: line two\n\nevent: error\ndata: oh no\n\n"
    respx.get("http://api.test/v1/runs/r1/logs").mock(
        return_value=httpx.Response(200, content=body, headers={"content-type": "text/event-stream"})
    )
    got = list(client.stream_logs("r1"))
    assert got == ["line one", "line two", "oh no"]
