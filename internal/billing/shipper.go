// Shipper polls the usage_events table and ships any new rows to a
// Destination (Orb in v1, more later). At-least-once semantics: the
// destination-side idempotency_key is derived deterministically from
// (event_id, kind) so retries are safe.

package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/shantanubansal/AiLab/internal/db"
)

// Shipper owns the polling loop.
type Shipper struct {
	Pool        *db.Pool
	Destination Destination
	BatchSize   int
	Interval    time.Duration
}

// NewShipper sets sensible defaults.
func NewShipper(pool *db.Pool, dst Destination) *Shipper {
	return &Shipper{
		Pool:        pool,
		Destination: dst,
		BatchSize:   200,
		Interval:    30 * time.Second,
	}
}

// Run blocks until ctx is canceled. Each tick reads a batch of new events
// past the checkpoint, ships them, and advances the checkpoint atomically
// only on success — the row stays at the prior point on failure so the
// next tick retries.
func (s *Shipper) Run(ctx context.Context) error {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.tick(ctx); err != nil {
				log.Printf("billing: ship tick: %v", err)
			}
		}
	}
}

func (s *Shipper) tick(ctx context.Context) error {
	dest := s.Destination.Name()
	var lastID int64
	if err := s.Pool.QueryRow(ctx, `
		SELECT last_event_id FROM usage_shipper_state WHERE destination = $1
	`, dest).Scan(&lastID); err != nil {
		return fmt.Errorf("checkpoint read: %w", err)
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT id, tenant_id, COALESCE(agent_id::text, ''), COALESCE(run_id::text, ''),
		       kind, quantity, recorded_at
		FROM usage_events
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, lastID, s.BatchSize)
	if err != nil {
		return err
	}
	type row struct {
		ID         int64
		TenantID   string
		AgentID    string
		RunID      string
		Kind       string
		Quantity   float64
		RecordedAt time.Time
	}
	var batch []row
	for rows.Next() {
		var r row
		var qty json.Number
		// Quantity is NUMERIC; pgx returns it as a pgtype. Scan into a
		// json.Number-friendly target via a manual cast.
		var qStr string
		if err := rows.Scan(&r.ID, &r.TenantID, &r.AgentID, &r.RunID, &r.Kind, &qStr, &r.RecordedAt); err != nil {
			rows.Close()
			return err
		}
		_ = qty
		if _, err := fmt.Sscanf(qStr, "%f", &r.Quantity); err != nil {
			// Be tolerant: ship with quantity=0 if the value is unparseable.
			r.Quantity = 0
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}

	events := make([]Event, 0, len(batch))
	for _, r := range batch {
		props := map[string]any{"quantity": r.Quantity}
		if r.AgentID != "" {
			props["agent_id"] = r.AgentID
		}
		if r.RunID != "" {
			props["run_id"] = r.RunID
		}
		events = append(events, Event{
			EventName:        r.Kind,
			ExternalCustomer: r.TenantID,
			Timestamp:        r.RecordedAt.UTC(),
			IdempotencyKey:   fmt.Sprintf("%s/%d", dest, r.ID),
			Properties:       props,
		})
	}

	shipCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.Destination.Ship(shipCtx, events); err != nil {
		return fmt.Errorf("ship: %w", err)
	}

	newCheckpoint := batch[len(batch)-1].ID
	_, err = s.Pool.Exec(ctx, `
		UPDATE usage_shipper_state
		SET last_event_id = $2, last_shipped_at = now()
		WHERE destination = $1 AND last_event_id < $2
	`, dest, newCheckpoint)
	if err != nil {
		return fmt.Errorf("advance checkpoint: %w", err)
	}
	log.Printf("billing: shipped %d events to %s (checkpoint %d → %d)", len(batch), dest, lastID, newCheckpoint)
	return nil
}
