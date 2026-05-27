"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { clearToken, getToken, setToken } from "@/lib/api";

const DEV_TENANT = "00000000-0000-0000-0000-000000000001";

export default function LoginPage() {
  const router = useRouter();
  const [current, setCurrent] = useState<string | null>(null);
  const [value, setValue] = useState<string>(`dev:${DEV_TENANT}:web-user`);

  useEffect(() => {
    setCurrent(getToken());
  }, []);

  function save() {
    setToken(value.trim());
    router.push("/agents");
  }

  function logout() {
    clearToken();
    setCurrent(null);
  }

  return (
    <div>
      <h1>Token</h1>
      <p className="muted">
        Pasted token is stored in localStorage and sent as <code>Bearer</code>.
        Dev mode accepts <code>dev:&lt;tenantId&gt;:&lt;userId&gt;</code>.
        Pre-filled value uses the seeded dev tenant.
      </p>

      {current ? (
        <div className="panel">
          <div className="row">
            <code style={{ flex: 1, wordBreak: "break-all" }}>{current}</code>
            <button className="danger" onClick={logout} style={{ flex: 0 }}>
              Clear
            </button>
          </div>
        </div>
      ) : (
        <p className="muted">No token stored.</p>
      )}

      <div className="panel">
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="dev:<tenantId>:<userId> or a real WorkOS JWT"
        />
        <div className="row" style={{ marginTop: 12 }}>
          <button onClick={save}>Save and continue</button>
        </div>
      </div>
    </div>
  );
}
