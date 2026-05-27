// Tiny api client. Reads the bearer token from localStorage and routes
// to NEXT_PUBLIC_API_BASE (or http://localhost:8080 in dev). All methods
// throw on non-2xx so callers can surface errors uniformly.

export const API_BASE =
  (typeof process !== "undefined" && process.env.NEXT_PUBLIC_API_BASE) ||
  "http://localhost:8080";

const TOKEN_KEY = "ailab.token";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(TOKEN_KEY);
}

function authHeader(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...authHeader(),
      ...(init.headers ?? {}),
    },
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status} ${res.statusText}: ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---- Types matching the api DTOs ----

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
  webhookSecret?: string; // only on create
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

// ---- Endpoints ----

export const api = {
  me: () => request<Me>("/v1/me"),
  listAgents: () => request<{ agents: Agent[] }>("/v1/agents").then((x) => x.agents),
  createAgent: (manifest: Record<string, unknown>) =>
    request<Agent>("/v1/agents", { method: "POST", body: JSON.stringify({ manifest }) }),
  getAgent: (id: string) => request<Agent>(`/v1/agents/${id}`),
  deleteAgent: (id: string) => request<void>(`/v1/agents/${id}`, { method: "DELETE" }),

  listRuns: (agentId: string) =>
    request<{ runs: Run[] }>(`/v1/agents/${agentId}/runs`).then((x) => x.runs),
  triggerRun: (agentId: string, inputs: Record<string, unknown> = {}) =>
    request<Run>(`/v1/agents/${agentId}/runs`, {
      method: "POST",
      body: JSON.stringify({ inputs }),
    }),
  getRun: (id: string) => request<Run>(`/v1/runs/${id}`),

  listTriggers: (agentId: string) =>
    request<{ triggers: Trigger[] }>(`/v1/agents/${agentId}/triggers`).then((x) => x.triggers),
  createTrigger: (agentId: string, body: { kind: "webhook" | "cron"; name: string; cron?: string }) =>
    request<Trigger>(`/v1/agents/${agentId}/triggers`, {
      method: "POST",
      body: JSON.stringify(body),
    }),

  deploy: (agentId: string) =>
    fetch(`${API_BASE}/v1/agents/${agentId}/deploy`, { method: "POST", headers: authHeader() }),
  undeploy: (agentId: string) =>
    fetch(`${API_BASE}/v1/agents/${agentId}/deploy`, { method: "DELETE", headers: authHeader() }),

  // streamEvents subscribes to /v1/runs/{id}/events. Each frame becomes
  // one onEvent call (started | completed) with the parsed payload.
  // Resolves when the stream closes (terminal run state) or signal aborts.
  streamEvents: async (
    runId: string,
    signal: AbortSignal,
    onEvent: (name: string, data: unknown) => void
  ) => {
    const res = await fetch(`${API_BASE}/v1/runs/${runId}/events`, {
      headers: authHeader(),
      signal,
    });
    if (!res.ok || !res.body) {
      throw new Error(`events: ${res.status} ${res.statusText}`);
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        let name = "message";
        let dataLine = "";
        for (const line of frame.split("\n")) {
          if (line.startsWith("event: ")) name = line.slice(7).trim();
          else if (line.startsWith("data: ")) dataLine = line.slice(6);
        }
        if (dataLine) {
          try {
            onEvent(name, JSON.parse(dataLine));
          } catch {
            // ignore unparseable frame
          }
        }
      }
    }
  },

  // streamLogs returns a ReadableStream that yields parsed SSE "data:" lines.
  // EventSource can't send Authorization headers, so we use fetch instead.
  streamLogs: async (runId: string, signal: AbortSignal, onLine: (line: string) => void) => {
    const res = await fetch(`${API_BASE}/v1/runs/${runId}/logs`, {
      headers: authHeader(),
      signal,
    });
    if (!res.ok || !res.body) {
      throw new Error(`logs: ${res.status} ${res.statusText}`);
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        for (const line of frame.split("\n")) {
          if (line.startsWith("data: ")) onLine(line.slice(6));
        }
      }
    }
  },
};
