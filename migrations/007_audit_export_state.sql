-- Checkpoint table for the audit S3 exporter. Mirrors the shape of
-- usage_shipper_state — one row per destination, advanced atomically
-- after each successful upload.

BEGIN;

CREATE TABLE IF NOT EXISTS audit_export_state (
    destination     TEXT PRIMARY KEY,
    last_event_id   BIGINT NOT NULL DEFAULT 0,
    last_exported_at TIMESTAMPTZ
);

INSERT INTO audit_export_state (destination, last_event_id)
VALUES ('s3', 0)
ON CONFLICT (destination) DO NOTHING;

COMMIT;
