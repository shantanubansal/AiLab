# python-llm template

A starter LLM agent that takes a `question` and returns an `answer` from Claude.

## Run locally

```bash
pip install -e .
export ANTHROPIC_API_KEY=sk-ant-...
echo '{"question": "What is the speed of light?"}' | python agent.py
```

The last line of stdout is your output JSON; anything on stderr is a log line.

## Run on the platform

```bash
ailab secrets set ANTHROPIC_API_KEY sk-ant-...
ailab agents create .              # uploads source, builder produces an image
ailab runs trigger hello-llm --input '{"question": "..."}'
ailab runs logs <run-id> --follow
```

## Contract recap

- **input**: validated input JSON arrives via the `AGENT_INPUTS` env var on the platform; for local-dev convenience the agent falls back to reading stdin if `AGENT_INPUTS` is unset. Shape declared in `uipath-agent.yaml` under `inputs`.
- **stdout last JSON line**: output JSON, shape declared under `outputs`.
- **stderr**: logs (Loki + UI).
- **AGENT_TRACE_ID** env: distributed trace id; propagate it into any client you call.
