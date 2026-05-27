"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { Agent, api, getToken } from "@/lib/api";

export default function AgentsPage() {
  const router = useRouter();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [image, setImage] = useState("hello-world");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!getToken()) {
      router.push("/login");
      return;
    }
    refresh();
  }, [router]);

  async function refresh() {
    setErr(null);
    try {
      setAgents(await api.listAgents());
    } catch (e) {
      setErr(String(e));
    }
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await api.createAgent({
        schemaVersion: "v1",
        name: name.trim(),
        mode: "job",
        runtime: "container",
        image: image.trim(),
      });
      setName("");
      await refresh();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <h1>Agents</h1>
      {err && <div className="error">{err}</div>}

      <div className="panel">
        <h2>Create</h2>
        <form onSubmit={create}>
          <div className="row">
            <input
              required
              placeholder="agent name (dns-1123)"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <input
              required
              placeholder="image"
              value={image}
              onChange={(e) => setImage(e.target.value)}
            />
            <button type="submit" disabled={busy} style={{ flex: 0 }}>
              {busy ? "..." : "Create"}
            </button>
          </div>
          <p className="muted" style={{ fontSize: 12 }}>
            v1 spine only supports <code>mode=job</code>, <code>runtime=container</code>.
            Code-agent + builder path lives in /v1/agents/&#123;id&#125;/builds.
          </p>
        </form>
      </div>

      <h2>All agents</h2>
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Mode</th>
            <th>Runtime</th>
            <th>Image</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {agents.map((a) => (
            <tr key={a.id}>
              <td>
                <Link href={`/agents/${a.id}`}>{a.name}</Link>
              </td>
              <td>{a.mode}</td>
              <td>{a.runtime}</td>
              <td>
                <code>{a.image ?? "—"}</code>
              </td>
              <td className="muted">{new Date(a.createdAt).toLocaleString()}</td>
            </tr>
          ))}
          {agents.length === 0 && (
            <tr>
              <td colSpan={5} className="muted">
                No agents yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
