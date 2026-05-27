-- Tracks the last usage_events.id the billing shipper has successfully
-- forwarded to Orb (or any future destination). Single-row table so the
-- shipper can fetch + UPDATE atomically.

BEGIN;

CREATE TABLE IF NOT EXISTS usage_shipper_state (
    destination     TEXT PRIMARY KEY,
    last_event_id   BIGINT NOT NULL DEFAULT 0,
    last_shipped_at TIMESTAMPTZ
);

INSERT INTO usage_shipper_state (destination, last_event_id)
VALUES ('orb', 0)
ON CONFLICT (destination) DO NOTHING;

COMMIT;
