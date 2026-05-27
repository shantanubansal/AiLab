# `@ailab/sdk` — TypeScript client

```bash
npm install
npm run build
```

```ts
import { Client } from "@ailab/sdk";

const cli = new Client({
  baseUrl: "http://localhost:8080",
  token: "dev:<tenantId>:<userId>",
});

const agent = await cli.createAgent({
  schemaVersion: "v1",
  name: "hello",
  mode: "job",
  runtime: "container",
  image: "hello-world",
});

const run = await cli.triggerRun(agent.id, { hi: "there" });

for await (const line of cli.streamLogs(run.id)) {
  console.log(line);
}

console.log(await cli.getRun(run.id));
```

Pure fetch — no transitive deps. Methods mirror `pkg/sdk-go/client.go`.
APIError carries `.status` and `.body` for non-2xx responses; the streaming
helper is an async generator so `for await ... of` cleans up the body
reader automatically when you break out.
