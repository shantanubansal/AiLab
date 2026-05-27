// StatusConsumer subscribes to run.started / run.completed events published
// by the controller and applies them to the runs table. It also writes
// metering rows to usage_events on the same transitions so v1.5 invoicing
// has the data it needs without any new producer-side work.
//
// Idempotency lives in the repos (MarkStarted / MarkCompleted are guarded
// by status WHERE clauses; usage inserts are guarded by a partial unique
// index on (run_id, kind)), so at-least-once delivery from JetStream is safe.

package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/usage"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// StatusConsumer applies controller-emitted run status messages.
type StatusConsumer struct {
	Repo   *Repo
	Usage  *usage.Repo
	Logger *zap.Logger
}

// Start subscribes to run.started and run.completed. It returns once the
// subscriptions are wired; messages are handled on the bus's goroutines.
func (c *StatusConsumer) Start(ctx context.Context, bus *eventbus.Bus) error {
	if err := bus.Subscribe(ctx, "ailab-api-run-started", events.SubjectRunStarted, c.handleStarted); err != nil {
		return fmt.Errorf("subscribe run.started: %w", err)
	}
	if err := bus.Subscribe(ctx, "ailab-api-run-completed", events.SubjectRunCompleted, c.handleCompleted); err != nil {
		return fmt.Errorf("subscribe run.completed: %w", err)
	}
	c.Logger.Info("status consumer subscribed")
	return nil
}

func (c *StatusConsumer) handleStarted(ctx context.Context, data []byte) error {
	var ev events.RunStarted
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal run.started: %w", err)
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := c.Repo.MarkStarted(ctx, ev.RunID, at); err != nil {
		c.Logger.Warn("mark started", zap.String("runId", ev.RunID), zap.Error(err))
		return err
	}
	if c.Usage != nil {
		if err := c.Usage.RecordRun(ctx, ev.TenantID, ev.AgentID, ev.RunID, usage.KindRunStart, 1, at); err != nil {
			c.Logger.Warn("usage run.start", zap.String("runId", ev.RunID), zap.Error(err))
		}
	}
	return nil
}

func (c *StatusConsumer) handleCompleted(ctx context.Context, data []byte) error {
	var ev events.RunCompleted
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal run.completed: %w", err)
	}
	endedAt := ev.EndedAt
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	}
	var exitPtr *int
	if ev.ExitCode != 0 {
		v := ev.ExitCode
		exitPtr = &v
	}
	st := mapStatus(ev.Status)
	if err := c.Repo.MarkCompleted(ctx, ev.RunID, st, ev.Outputs, exitPtr, ev.Error, endedAt); err != nil {
		c.Logger.Warn("mark completed", zap.String("runId", ev.RunID), zap.Error(err))
		return err
	}
	if c.Usage != nil {
		// run.end as a counter, run.seconds as the duration. Both ignore
		// retries via the (run_id, kind) unique constraint.
		_ = c.Usage.RecordRun(ctx, ev.TenantID, ev.AgentID, ev.RunID, usage.KindRunEnd, 1, endedAt)
		seconds := endedAt.Sub(ev.StartedAt).Seconds()
		if seconds < 0 {
			seconds = 0
		}
		_ = c.Usage.RecordRun(ctx, ev.TenantID, ev.AgentID, ev.RunID, usage.KindRunSeconds, seconds, endedAt)
	}
	return nil
}

func mapStatus(s events.RunStatus) Status {
	switch s {
	case events.RunStatusSucceeded:
		return StatusSucceeded
	case events.RunStatusFailed:
		return StatusFailed
	case events.RunStatusTimedOut:
		return StatusTimedOut
	case events.RunStatusCancelled:
		return StatusCancelled
	}
	return StatusFailed
}
