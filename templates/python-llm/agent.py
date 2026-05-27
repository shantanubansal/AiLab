"""Hello-LLM agent template.

The platform contract:
  - validated input JSON arrives via the AGENT_INPUTS env var (cluster path)
    or on stdin (local-dev convenience)
  - your final output JSON is the last line on stdout
  - logs go to stderr (captured into Loki, viewable in the UI)
  - the env var AGENT_TRACE_ID, if set, ties your logs to a distributed trace
"""

from __future__ import annotations

import json
import os
import sys

import anthropic


MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def read_inputs() -> dict:
    raw = os.environ.get("AGENT_INPUTS", "").strip()
    if not raw:
        raw = sys.stdin.read().strip()
    return json.loads(raw) if raw else {}


def main() -> int:
    payload = read_inputs()
    question = payload.get("question", "Say hello.")

    trace_id = os.environ.get("AGENT_TRACE_ID", "")
    log(f"trace={trace_id} model={MODEL} question={question!r}")

    client = anthropic.Anthropic()
    resp = client.messages.create(
        model=MODEL,
        max_tokens=512,
        messages=[{"role": "user", "content": question}],
    )

    text_parts = [block.text for block in resp.content if getattr(block, "type", "") == "text"]
    answer = "".join(text_parts).strip()

    print(json.dumps({"answer": answer}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
