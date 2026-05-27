# ts-llm template

A TypeScript LLM agent that takes a `question` and returns an `answer` from Claude.

## Run locally

```bash
npm install
export ANTHROPIC_API_KEY=sk-ant-...
echo '{"question":"What is the speed of light?"}' | npm start
```

The last JSON line on stdout is your output. stderr is logs.

## Run on the platform

```bash
ailab secrets set ANTHROPIC_API_KEY sk-ant-...
agentctl init ts-llm                   # if you haven't already
# edit src/agent.ts as needed, then:
agentctl agents create -f uipath-agent.yaml --image <pre-built-image>
# or kick off a builder run that ships the image for you:
agentctl builds create <agentId> --source git+https://github.com/me/my-agent.git
```

## Contract

- **input**: `AGENT_INPUTS` env (platform) or stdin (local). Shape in `uipath-agent.yaml` → `inputs`.
- **output**: last JSON line on stdout. Shape in `uipath-agent.yaml` → `outputs`.
- **logs**: stderr.
- **AGENT_TRACE_ID**: distributed trace id; propagate into outbound calls.
