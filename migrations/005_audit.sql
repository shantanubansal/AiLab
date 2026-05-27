-- Append-only audit trail. Every state-changing write goes here; reads
-- and listing endpoints are intentionally exempt to keep volume bounded.
--
-- The metadata column is a small JSONB blob with whatever non-PII context
-- the handler attached (e.g. {"image":"...", "manifest":{...}}).

BEGIN;

CREATE TABLE IF NOT EXISTS audit_events (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       TEXT,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT,
    metadata      JSONB,
    request_id    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_tenant_time_idx
    ON audit_events(tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS audit_tenant_resource_idx
    ON audit_events(tenant_id, resource_type, resource_id);

COMMIT;
