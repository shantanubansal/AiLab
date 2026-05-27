"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { Agent, Run, Trigger, api, getToken } from "@/lib/api";

function statusPill(status: Run["status"]) {
  const cls =
    status === "succeeded" ? "ok" : status === "failed" || status === "timed_out" ? "bad" : "run";
  return <span className={`pill ${cls}`}>{status}</span>;
}

export default function AgentDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const router = useRouter();

  const [agent, setAgent] = useState<Agent | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [triggers, setTriggers] = useState<Trigger[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [inputsRaw, setInputsRaw] = useState("{}");
  const [trigName, setTrigName] = useState("");
  const [trigKind, setTrigKind] = useState<"webhook" | "cron">("webhook");
  const [trigCron, setTrigCron] = useState("*/5 * * * *");
  const [revealedSecret, setRevealedSecret] = useState<string | null>(null);

  useEffect(() => {
    if (!getToken()) {
      router.push("/login");
      return;
    }
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [id]);

  async function refresh() {
    if (!id) return;
    try {
      const [a, rs, ts] = await Promise.all([
        api.getAgent(id),
        api.listRuns(id),
        api.listTriggers(id),
      ]);
      setAgent(a);
      setRuns(rs);
      setTriggers(ts);
      setErr(null);
    } catch (e) {
      setErr(String(e));
    }
  }

  async function triggerRun() {
    setBusy(true);
    setErr(null);
    try {
      let parsed: Record<string, unknown> = {};
      if (inputsRaw.trim()) {
        parsed = JSON.parse(inputsRaw);
      }
      const run = await api.triggerRun(id, parsed);
      router.push(`/runs/${run.id}`);
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function createTrigger() {
    setBusy(true);
    setErr(null);
    setRevealedSecret(null);
    try {
      const t = await api.createTrigger(id, {
        kind: trigKind,
        name: trigName,
        cron: trigKind === "cron" ? trigCron : undefined,
      });
      if (t.webhookSecret) setRevealedSecret(t.webhookSecret);
      setTrigName("");
      await refresh();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!agent) return <p className="muted">Loading…</p>;

  return (
    <div>
      <h1>
        {agent.name} <span className="muted">({agent.mode}/{agent.runtime})</span>
      </h1>
      {err && <div className="error">{err}</div>}
      <p>
        <code>{agent.image ?? "no image"}</code>
        <span className="muted" style={{ marginLeft: 12 }}>
          {new Date(agent.createdAt).toLocaleString()}
        </span>
      </p>

      <div className="panel">
        <h2>Run manually</h2>
        <textarea
          rows={3}
          value={inputsRaw}
          onChange={(e) => setInputsRaw(e.target.value)}
          placeholder="inputs JSON"
        />
        <div className="row" style={{ marginTop: 8 }}>
          <button onClick={triggerRun} disabled={busy} style={{ flex: 0 }}>
            {busy ? "..." : "Run"}
          </button>
        </div>
      </div>

      <h2>Recent runs</h2>
      <table>
        <thead>
          <tr>
            <th>Status</th>
            <th>ID</th>
            <th>Started</th>
            <th>Ended</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id}>
              <td>{statusPill(r.status)}</td>
              <td>
                <Link href={`/runs/${r.id}`}>
                  <code>{r.id.slice(0, 8)}</code>
                </Link>
              </td>
              <td className="muted">{r.startedAt ? new Date(r.startedAt).toLocaleTimeString() : "—"}</td>
              <td className="muted">{r.endedAt ? new Date(r.endedAt).toLocaleTimeString() : "—"}</td>
            </tr>
          ))}
          {runs.length === 0 && (
            <tr>
              <td colSpan={4} className="muted">
                No runs yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <div className="panel" style={{ marginTop: 24 }}>
        <h2>Triggers</h2>
        <table>
          <thead>
            <tr>
              <th>Kind</th>
              <th>Name</th>
              <th>Cron</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {triggers.map((t) => (
              <tr key={t.id}>
                <td>
                  <span className="pill">{t.kind}</span>
                </td>
                <td>{t.name}</td>
                <td>
                  <code>{t.cron ?? "—"}</code>
                </td>
                <td className="muted">{new Date(t.createdAt).toLocaleString()}</td>
              </tr>
            ))}
            {triggers.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  No triggers yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>

        <h2>New trigger</h2>
        <div className="row">
          <select value={trigKind} onChange={(e) => setTrigKind(e.target.value as "webhook" | "cron")}>
            <option value="webhook">webhook</option>
            <option value="cron">cron</option>
          </select>
          <input
            value={trigName}
            onChange={(e) => setTrigName(e.target.value)}
            placeholder="name (e.g. incoming)"
          />
          {trigKind === "cron" && (
            <input
              value={trigCron}
              onChange={(e) => setTrigCron(e.target.value)}
              placeholder="cron expression"
            />
          )}
          <button onClick={createTrigger} disabled={busy || !trigName} style={{ flex: 0 }}>
            Create
          </button>
        </div>
        {revealedSecret && (
          <div className="panel" style={{ marginTop: 12 }}>
            <p className="muted">Webhook secret (shown once — copy it now):</p>
            <code style={{ wordBreak: "break-all" }}>{revealedSecret}</code>
          </div>
        )}
      </div>
    </div>
  );
}
