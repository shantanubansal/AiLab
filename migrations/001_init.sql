-- Initial schema for the control plane.
--
-- v1 keeps the surface narrow: tenants, agents, runs. Builds / secrets /
-- triggers will arrive as separate migrations once their HTTP surfaces land.
--
-- Conventions:
--   * Every business-data row carries tenant_id and is queried through a
--     tenant-scoped repository. RLS will be layered on later; for now the
--     guard is in Go.
--   * Timestamps are UTC. created_at defaults to now(); started_at/ended_at
--     are populated by the controller via the run-status feedback loop.

BEGIN;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS tenants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    mode       TEXT NOT NULL CHECK (mode IN ('job', 'server')),
    runtime    TEXT NOT NULL CHECK (runtime IN ('python', 'typescript', 'container')),
    image      TEXT,
    manifest   JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS agents_tenant_idx ON agents(tenant_id);

CREATE TABLE IF NOT EXISTS runs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','timed_out','cancelled')),
    inputs     JSONB,
    outputs    JSONB,
    exit_code  INTEGER,
    error      TEXT,
    trace_id   TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    ended_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS runs_tenant_idx     ON runs(tenant_id);
CREATE INDEX IF NOT EXISTS runs_agent_idx      ON runs(agent_id);
CREATE INDEX IF NOT EXISTS runs_created_at_idx ON runs(created_at DESC);

-- Seed a dev tenant that matches the auth bypass token format
-- (Bearer dev:00000000-0000-0000-0000-000000000001:<userId>).
INSERT INTO tenants (id, slug, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'dev', 'Local development tenant')
ON CONFLICT (id) DO NOTHING;

COMMIT;
