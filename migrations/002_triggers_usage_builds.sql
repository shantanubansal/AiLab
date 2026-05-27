-- v1.1 schema additions:
--   * triggers — declarative trigger config per agent (webhook, cron).
--     Webhook trigger stores only an HMAC secret hash; the plaintext is
--     surfaced once at creation, never read back.
--   * builds — one row per build request; status lifecycle mirrors runs.
--   * usage_events — append-only metering log. Aggregations roll up to
--     usage_daily later. No invoicing in v1, plumbing only.

BEGIN;

CREATE TABLE IF NOT EXISTS triggers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id            UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind                TEXT NOT NULL CHECK (kind IN ('webhook','cron')),
    name                TEXT NOT NULL,
    webhook_secret_hash TEXT,                              -- bcrypt of the plaintext secret; only set for kind=webhook
    cron_expr           TEXT,                              -- standard 5-field cron; only set for kind=cron
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, name)
);

CREATE INDEX IF NOT EXISTS triggers_agent_idx  ON triggers(agent_id);
CREATE INDEX IF NOT EXISTS triggers_tenant_idx ON triggers(tenant_id);

CREATE TABLE IF NOT EXISTS builds (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','blocked')),
    source_url TEXT NOT NULL,
    image      TEXT,
    error      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS builds_agent_idx  ON builds(agent_id);
CREATE INDEX IF NOT EXISTS builds_tenant_idx ON builds(tenant_id);

CREATE TABLE IF NOT EXISTS usage_events (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id    UUID REFERENCES agents(id) ON DELETE SET NULL,
    run_id      UUID REFERENCES runs(id)   ON DELETE SET NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('run.start','run.end','run.seconds','mcp.heartbeat','build.minutes')),
    quantity    NUMERIC NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS usage_events_tenant_time_idx ON usage_events(tenant_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS usage_events_run_idx         ON usage_events(run_id);

-- Idempotency guard: at-least-once status events from the controller must
-- not double-count run.start / run.end / run.seconds for the same run.
CREATE UNIQUE INDEX IF NOT EXISTS usage_events_run_kind_unique
    ON usage_events(run_id, kind)
    WHERE run_id IS NOT NULL;

COMMIT;
