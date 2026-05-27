// Tests use a stub fetch — no network. Verifies bearer auth, JSON
// encoding, error mapping, and SSE log frame parsing.

import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { APIError, Client } from "../src/index.ts";

type StubCall = {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
};

function makeStub(handler: (call: StubCall) => Response | Promise<Response>) {
  const calls: StubCall[] = [];
  const stub: typeof fetch = async (input, init) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? "GET").toUpperCase();
    const headers: Record<string, string> = {};
    if (init?.headers) {
      const h = init.headers as Record<string, string>;
      for (const k of Object.keys(h)) headers[k.toLowerCase()] = h[k];
    }
    const body = typeof init?.body === "string" ? init.body : undefined;
    const call: StubCall = { url, method, headers, body };
    calls.push(call);
    return handler(call);
  };
  return { stub, calls };
}

describe("Client", () => {
  it("sends bearer + parses agents list", async () => {
    const { stub, calls } = makeStub(() =>
      new Response(JSON.stringify({ agents: [{ id: "a1", name: "x" }] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    );
    const cli = new Client({ baseUrl: "http://api.test", token: "tok-1", fetch: stub });
    const out = await cli.listAgents();
    assert.deepEqual(out, [{ id: "a1", name: "x" }]);
    assert.equal(calls[0].url, "http://api.test/v1/agents");
    assert.equal(calls[0].headers["authorization"], "Bearer tok-1");
  });

  it("posts manifest as JSON", async () => {
    const { stub, calls } = makeStub(
      () => new Response(JSON.stringify({ id: "a1" }), { status: 201 })
    );
    const cli = new Client({ baseUrl: "http://api.test", token: "tok", fetch: stub });
    const out = await cli.createAgent({ name: "x" });
    assert.deepEqual(out, { id: "a1" });
    assert.equal(calls[0].method, "POST");
    assert.equal(calls[0].body, JSON.stringify({ manifest: { name: "x" } }));
    assert.equal(calls[0].headers["content-type"], "application/json");
  });

  it("throws APIError with status + body on non-2xx", async () => {
    const { stub } = makeStub(() => new Response("not found", { status: 404 }));
    const cli = new Client({ baseUrl: "http://api.test", token: "tok", fetch: stub });
    await assert.rejects(
      () => cli.getAgent("missing"),
      (e: unknown) => {
        const err = e as APIError;
        return err.status === 404 && err.body.includes("not found");
      }
    );
  });

  it("streams SSE data frames", async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder();
        controller.enqueue(enc.encode("data: line one\n\n"));
        controller.enqueue(enc.encode("data: line two\n\nevent: error\ndata: oh no\n\n"));
        controller.close();
      },
    });
    const { stub } = makeStub(
      () => new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } })
    );
    const cli = new Client({ baseUrl: "http://api.test", token: "tok", fetch: stub });
    const lines: string[] = [];
    for await (const l of cli.streamLogs("r1")) lines.push(l);
    assert.deepEqual(lines, ["line one", "line two", "oh no"]);
  });
});
