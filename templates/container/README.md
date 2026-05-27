# container template

Bring your own prebuilt OCI image. The platform doesn't build for you;
it just runs the `image` you reference in `uipath-agent.yaml`.

## Contract

Your container must:

- Read validated input JSON from the `AGENT_INPUTS` environment variable.
- Write a final JSON object on stdout (last valid JSON line).
- Use stderr for logs (captured by Loki, streamed to the UI).
- Honor `AGENT_TRACE_ID` for distributed tracing where applicable.
- Exit 0 on success, non-zero on failure.

## Deploy

```bash
# edit uipath-agent.yaml: change `image:` to your pushed image
agentctl agents create -f uipath-agent.yaml
agentctl runs trigger <agentId> --inputs '{"any":"json"}'
agentctl runs logs <runId>
```

## When to use this template

When your agent is in a language we don't have a first-class template for,
or when you have a pre-existing image you want to run on the platform's
substrate without changing the build pipeline. For Python/TypeScript with
the Anthropic SDK, use `python-llm` or `ts-llm` instead — they're built
for you by the platform's Kaniko-based builder.
