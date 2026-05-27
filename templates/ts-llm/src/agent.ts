// TypeScript LLM agent template.
//
// Platform contract:
//   * validated input JSON arrives via the AGENT_INPUTS env var
//     (cluster path) or on stdin (local-dev fallback)
//   * the final output JSON is the last line on stdout
//   * logs go to stderr (captured into Loki, viewable in the UI)
//   * the env var AGENT_TRACE_ID, if set, ties logs to a distributed trace

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env.MODEL ?? "claude-sonnet-4-6";

function log(msg: string): void {
  process.stderr.write(msg + "\n");
}

async function readInputs(): Promise<Record<string, unknown>> {
  const fromEnv = (process.env.AGENT_INPUTS ?? "").trim();
  if (fromEnv) return JSON.parse(fromEnv);
  const chunks: Buffer[] = [];
  for await (const c of process.stdin) chunks.push(c as Buffer);
  const raw = Buffer.concat(chunks).toString("utf8").trim();
  return raw ? JSON.parse(raw) : {};
}

async function main(): Promise<void> {
  const inputs = await readInputs();
  const question = String(inputs.question ?? "Say hello.");

  log(`trace=${process.env.AGENT_TRACE_ID ?? ""} model=${MODEL} question=${JSON.stringify(question)}`);

  const client = new Anthropic();
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 512,
    messages: [{ role: "user", content: question }],
  });

  const answer = resp.content
    .filter((b) => b.type === "text")
    .map((b) => (b as { type: "text"; text: string }).text)
    .join("")
    .trim();

  process.stdout.write(JSON.stringify({ answer }) + "\n");
}

main().catch((e) => {
  log("error: " + (e as Error).stack);
  process.exit(1);
});
