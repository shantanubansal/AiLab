"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { Run, api, getToken } from "@/lib/api";

function statusPill(status: Run["status"]) {
  const cls =
    status === "succeeded" ? "ok" : status === "failed" || status === "timed_out" ? "bad" : "run";
  return <span className={`pill ${cls}`}>{status}</span>;
}

export default function RunDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const router = useRouter();

  const [run, setRun] = useState<Run | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [logsErr, setLogsErr] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const logsRef = useRef<HTMLPreElement>(null);

  // Status polling — runs until the run reaches a terminal state.
  useEffect(() => {
    if (!getToken()) {
      router.push("/login");
      return;
    }
    let stopped = false;
    async function tick() {
      try {
        const r = await api.getRun(id);
        if (stopped) return;
        setRun(r);
        if (["pending", "running"].includes(r.status)) {
          setTimeout(tick, 1500);
        }
      } catch (e) {
        if (!stopped) setErr(String(e));
      }
    }
    tick();
    return () => {
      stopped = true;
    };
  }, [id, router]);

  // SSE log stream — runs in parallel; api streams until pod exits.
  useEffect(() => {
    if (!id || !getToken()) return;
    const ctrl = new AbortController();
    api
      .streamLogs(id, ctrl.signal, (line) => {
        setLogs((prev) => [...prev, line]);
        // Auto-scroll
        requestAnimationFrame(() => {
          if (logsRef.current) logsRef.current.scrollTop = logsRef.current.scrollHeight;
        });
      })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        setLogsErr(String(e));
      });
    return () => ctrl.abort();
  }, [id]);

  if (!run) return <p className="muted">Loading…</p>;

  return (
    <div>
      <h1>
        Run <code>{run.id.slice(0, 8)}</code> {statusPill(run.status)}
      </h1>
      {err && <div className="error">{err}</div>}
      <p className="muted">
        Created {new Date(run.createdAt).toLocaleString()}
        {run.startedAt && ` · started ${new Date(run.startedAt).toLocaleTimeString()}`}
        {run.endedAt && ` · ended ${new Date(run.endedAt).toLocaleTimeString()}`}
      </p>

      <div className="panel">
        <h2>Inputs</h2>
        <pre className="logs" style={{ height: "auto", minHeight: 60 }}>
          {JSON.stringify(run.inputs ?? {}, null, 2)}
        </pre>
        <h2>Outputs</h2>
        <pre className="logs" style={{ height: "auto", minHeight: 60 }}>
          {JSON.stringify(run.outputs ?? {}, null, 2)}
        </pre>
      </div>

      <h2>Logs (live)</h2>
      {logsErr && <div className="error">{logsErr}</div>}
      <pre className="logs" ref={logsRef}>
        {logs.join("\n") || "(waiting for pod to start...)"}
      </pre>
    </div>
  );
}
