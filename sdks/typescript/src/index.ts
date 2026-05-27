// TypeScript SDK for the AiLab platform API.
//
// Pure fetch (no transitive deps). Streaming logs uses ReadableStream from
// the response body, parsing SSE "data:" frames as they arrive.

export type Agent = {
  id: string;
  name: string;
  mode: "job" | "server";
  runtime: "python" | "typescript" | "container";
  image: string | null;
  createdAt: string;
};

export type Run = {
  id: string;
  agentId: string;
  status: "pending" | "running" | "succeeded" | "failed" | "timed_out" | "cancelled";
  inputs?: Record<string, unknown>;
  outputs?: Record<string, unknown>;
  exitCode?: number;
  traceId?: string;
  createdAt: string;
  startedAt?: string;
  endedAt?: string;
};

export type Trigger = {
  id: string;
  agentId: string;
  kind: "webhook" | "cron";
  name: string;
  cron?: string;
  createdAt: string;
  webhookSecret?: string;
};

export type Build = {
  id: string;
  agentId: string;
  status: "pending" | "running" | "succeeded" | "failed" | "blocked";
  image?: string;
  createdAt: string;
  endedAt?: string;
};

export type SecretRef = {
  name: string;
  createdAt: string;
  updatedAt: string;
};

export type Me = {
  userId: string;
  seatCount: number;
  tenant: {
    id: string;
    slug: string;
    name: string;
    createdAt: string;
  };
};

export class APIError extends Error {
  constructor(public readonly status: number, public readonly body: string) {
    super(`api ${status}: ${body}`);
  }
}

export type ClientOptions = {
  baseUrl?: string;
  token: string;
  fetch?: typeof fetch;
};

export class Client {
  private readonly baseUrl: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;

  constructor(opts: ClientOptions) {
    if (!opts.token) throw new Error("token is required");
    this.baseUrl = (opts.baseUrl ?? "http://localhost:8080").replace(/\/+$/, "");
    this.token = opts.token;
    this.fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
  }

  // ---- Agents ----
  async listAgents(): Promise<Agent[]> {
    const { agents } = await this.request<{ agents: Agent[] }>("GET", "/v1/agents");
    return agents;
  }

  createAgent(manifest: Record<string, unknown>): Promise<Agent> {
    return this.request<Agent>("POST", "/v1/agents", { manifest });
  }

  getAgent(id: string): Promise<Agent> {
    return this.request<Agent>("GET", `/v1/agents/${id}`);
  }

  async deleteAgent(id: string): Promise<void> {
    await this.request<void>("DELETE", `/v1/agents/${id}`);
  }

  // ---- Runs ----
  async listRuns(agentId: string): Promise<Run[]> {
    const { runs } = await this.request<{ runs: Run[] }>("GET", `/v1/agents/${agentId}/runs`);
    return runs;
  }

  triggerRun(agentId: string, inputs: Record<string, unknown> = {}): Promise<Run> {
    return this.request<Run>("POST", `/v1/agents/${agentId}/runs`, { inputs });
  }

  getRun(id: string): Promise<Run> {
    return this.request<Run>("GET", `/v1/runs/${id}`);
  }

  /**
   * Stream logs as SSE "data:" lines. The async generator ends when the
   * server closes the stream (e.g. pod exits) or when the caller breaks out.
   */
  async *streamLogs(runId: string, init: { signal?: AbortSignal } = {}): AsyncGenerator<string> {
    const res = await this.fetchImpl(`${this.baseUrl}/v1/runs/${runId}/logs`, {
      headers: this.headers(false),
      signal: init.signal,
    });
    if (!res.ok || !res.body) {
      throw new APIError(res.status, (await safeText(res)).trim());
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let idx: number;
        while ((idx = buffer.indexOf("\n\n")) >= 0) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          for (const line of frame.split("\n")) {
            if (line.startsWith("data: ")) yield line.slice(6);
          }
        }
      }
    } finally {
      try {
        reader.releaseLock();
      } catch {
        // ignore
      }
    }
  }

  // ---- Triggers ----
  async listTriggers(agentId: string): Promise<Trigger[]> {
    const { triggers } = await this.request<{ triggers: Trigger[] }>("GET", `/v1/agents/${agentId}/triggers`);
    return triggers;
  }

  createWebhookTrigger(agentId: string, name: string): Promise<Trigger> {
    return this.request<Trigger>("POST", `/v1/agents/${agentId}/triggers`, {
      kind: "webhook",
      name,
    });
  }

  createCronTrigger(agentId: string, name: string, cronExpr: string): Promise<Trigger> {
    return this.request<Trigger>("POST", `/v1/agents/${agentId}/triggers`, {
      kind: "cron",
      name,
      cron: cronExpr,
    });
  }

  // ---- Deploy ----
  async deploy(agentId: string): Promise<void> {
    await this.request<void>("POST", `/v1/agents/${agentId}/deploy`);
  }

  async undeploy(agentId: string): Promise<void> {
    await this.request<void>("DELETE", `/v1/agents/${agentId}/deploy`);
  }

  // ---- Builds ----
  createBuild(agentId: string, sourceUrl: string): Promise<Build> {
    return this.request<Build>("POST", `/v1/agents/${agentId}/builds`, { sourceUrl });
  }

  // ---- Secrets ----
  async listSecrets(): Promise<SecretRef[]> {
    const { secrets } = await this.request<{ secrets: SecretRef[] }>("GET", "/v1/secrets");
    return secrets;
  }

  setSecret(name: string, value: string): Promise<SecretRef> {
    return this.request<SecretRef>("POST", "/v1/secrets", { name, value });
  }

  async deleteSecret(name: string): Promise<void> {
    await this.request<void>("DELETE", `/v1/secrets/${name}`);
  }

  // ---- /v1/me ----
  me(): Promise<Me> {
    return this.request<Me>("GET", "/v1/me");
  }

  // ---- internals ----
  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      headers: this.headers(body !== undefined),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      throw new APIError(res.status, (await safeText(res)).trim());
    }
    if (res.status === 204) return undefined as T;
    const text = await safeText(res);
    if (!text) return undefined as T;
    return JSON.parse(text) as T;
  }

  private headers(json: boolean): Record<string, string> {
    const h: Record<string, string> = { Authorization: `Bearer ${this.token}` };
    if (json) h["Content-Type"] = "application/json";
    return h;
  }
}

async function safeText(res: Response): Promise<string> {
  try {
    return await res.text();
  } catch {
    return "";
  }
}
