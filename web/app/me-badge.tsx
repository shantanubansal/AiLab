"use client";

import { useEffect, useState } from "react";

import { Me, api, getToken } from "@/lib/api";

// Tiny client component that hits /v1/me once on mount and renders the
// tenant name + user id. Used in the layout header. Renders nothing
// until the call resolves so we don't flash placeholder text.
export function MeBadge() {
  const [me, setMe] = useState<Me | null>(null);

  useEffect(() => {
    if (!getToken()) return;
    api
      .me()
      .then(setMe)
      .catch(() => {
        // Surface nothing on failure — the header is decorative; the
        // /login page handles auth issues for real.
      });
  }, []);

  if (!me) return null;
  return (
    <span className="muted" style={{ fontSize: 12 }}>
      {me.tenant.name} · <code>{me.userId}</code>
    </span>
  );
}
