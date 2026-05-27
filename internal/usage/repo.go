// Package usage is the append-only metering log. The controller writes
// here on every run phase transition; v1.5 layers Stripe/Orb invoicing on
// top of the same rows without changes to the producer side.
package usage

import (
	"context"
	"time"

	"github.com/shantanubansal/AiLab/internal/db"
)

// Kind enumerates the v1 metering events. Mirrors the CHECK constraint
// in migrations/002.
type Kind string

const (
	KindRunStart      Kind = "run.start"
	KindRunEnd        Kind = "run.end"
	KindRunSeconds    Kind = "run.seconds"
	KindMCPHeartbeat  Kind = "mcp.heartbeat"
	KindBuildMinutes  Kind = "build.minutes"
)

// Repo writes usage events. Inserts are idempotent on (run_id, kind) via
// the partial unique index on the table.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// RecordRun writes a per-run usage event. quantity units depend on kind:
//   run.start, run.end          → 1 (counter)
//   run.seconds                 → seconds (float)
// The ON CONFLICT clause makes redelivery a no-op.
func (r *Repo) RecordRun(ctx context.Context, tenantID, agentID, runID string, kind Kind, quantity float64, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO usage_events (tenant_id, agent_id, run_id, kind, quantity, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (run_id, kind) WHERE run_id IS NOT NULL DO NOTHING
	`, tenantID, agentID, runID, string(kind), quantity, at)
	return err
}
