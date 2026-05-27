-- Per-tenant secrets table. Values are AES-GCM ciphertext under
-- API_SECRETS_KEY (see internal/cryptobox). v1.5 swaps the column to a
-- KMS data-key reference.
--
-- The api never returns the plaintext on GET — only at the original POST
-- response and at run materialization time, when it projects the value
-- into a k8s Secret owned by the run / agent.

BEGIN;

CREATE TABLE IF NOT EXISTS secrets (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    value_ciphertext TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS secrets_tenant_idx ON secrets(tenant_id);

COMMIT;
