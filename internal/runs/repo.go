// Postgres-backed repository for the runs table. The Run model here is the
// control-plane state (api-side). It is distinct from the AgentRun CRD in
// types.go, which is the controller-runtime view used by the reconciler.
//
// Flow:
//   api creates Run{status:pending} via Repo.Create → publishes run.requested
//   → controller materializes an AgentRun CR → reconciler runs a Job →
//   reconciler publishes run.started / run.completed → api updates this row
//   via Repo.MarkStarted / Repo.MarkCompleted.

package runs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/db"
)

// Status mirrors the runs.status check constraint in 001_init.sql.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimedOut  Status = "timed_out"
	StatusCancelled Status = "cancelled"
)

// Run is one row of the runs table — the api-side representation.
type Run struct {
	ID        string
	TenantID  string
	AgentID   string
	Status    Status
	Inputs    map[string]any
	Outputs   map[string]any
	ExitCode  *int
	Error     string
	TraceID   string
	CreatedAt time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
}

// Repo is the runs repository. Construct with NewRepo.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// Create inserts a pending run for the given agent and returns it. Inputs
// may be nil; they're persisted as JSON either way.
func (r *Repo) Create(ctx context.Context, tenantID, agentID string, inputs map[string]any, traceID string) (Run, error) {
	var inputsJSON []byte
	if inputs != nil {
		b, err := json.Marshal(inputs)
		if err != nil {
			return Run{}, err
		}
		inputsJSON = b
	}
	var out Run
	err := r.pool.QueryRow(ctx, `
		INSERT INTO runs (tenant_id, agent_id, status, inputs, trace_id)
		VALUES ($1, $2, 'pending', $3, NULLIF($4, ''))
		RETURNING id, tenant_id, agent_id, status, created_at, trace_id
	`, tenantID, agentID, inputsJSON, traceID).
		Scan(&out.ID, &out.TenantID, &out.AgentID, &out.Status, &out.CreatedAt, &nullString{&out.TraceID})
	if err != nil {
		return Run{}, err
	}
	out.Inputs = inputs
	return out, nil
}

// Get fetches a run by id, scoped to the tenant.
func (r *Repo) Get(ctx context.Context, tenantID, id string) (Run, error) {
	var run Run
	var inputs, outputs []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, agent_id, status, inputs, outputs, exit_code,
		       COALESCE(error, ''), COALESCE(trace_id, ''),
		       created_at, started_at, ended_at
		FROM runs
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&run.ID, &run.TenantID, &run.AgentID, &run.Status,
		&inputs, &outputs, &run.ExitCode, &run.Error, &run.TraceID,
		&run.CreatedAt, &run.StartedAt, &run.EndedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, db.ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if len(inputs) > 0 {
		_ = json.Unmarshal(inputs, &run.Inputs)
	}
	if len(outputs) > 0 {
		_ = json.Unmarshal(outputs, &run.Outputs)
	}
	return run, nil
}

// ListForAgent returns recent runs of an agent, newest first.
func (r *Repo) ListForAgent(ctx context.Context, tenantID, agentID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, agent_id, status, inputs, outputs, exit_code,
		       COALESCE(error, ''), COALESCE(trace_id, ''),
		       created_at, started_at, ended_at
		FROM runs
		WHERE tenant_id = $1 AND agent_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, tenantID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var run Run
		var inputs, outputs []byte
		if err := rows.Scan(
			&run.ID, &run.TenantID, &run.AgentID, &run.Status,
			&inputs, &outputs, &run.ExitCode, &run.Error, &run.TraceID,
			&run.CreatedAt, &run.StartedAt, &run.EndedAt,
		); err != nil {
			return nil, err
		}
		if len(inputs) > 0 {
			_ = json.Unmarshal(inputs, &run.Inputs)
		}
		if len(outputs) > 0 {
			_ = json.Unmarshal(outputs, &run.Outputs)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// MarkStarted transitions a pending run to running and stamps started_at.
// Idempotent: a no-op if the run already has a started_at.
func (r *Repo) MarkStarted(ctx context.Context, runID string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE runs
		SET status = 'running', started_at = COALESCE(started_at, $2)
		WHERE id = $1 AND status IN ('pending','running')
	`, runID, at)
	return err
}

// MarkCompleted writes the terminal state for a run. Idempotent on retry —
// the WHERE clause guards against overwriting an already-terminal row.
func (r *Repo) MarkCompleted(ctx context.Context, runID string, status Status, outputs map[string]any, exitCode *int, errMsg string, endedAt time.Time) error {
	var outputsJSON []byte
	if outputs != nil {
		b, err := json.Marshal(outputs)
		if err != nil {
			return err
		}
		outputsJSON = b
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE runs
		SET status = $2,
		    outputs = $3,
		    exit_code = $4,
		    error = NULLIF($5, ''),
		    ended_at = $6
		WHERE id = $1 AND status NOT IN ('succeeded','failed','timed_out','cancelled')
	`, runID, string(status), outputsJSON, exitCode, errMsg, endedAt)
	return err
}

// nullString is a small Scanner that maps SQL NULL to "".
type nullString struct{ s *string }

func (n *nullString) Scan(src any) error {
	if src == nil {
		*n.s = ""
		return nil
	}
	switch v := src.(type) {
	case string:
		*n.s = v
	case []byte:
		*n.s = string(v)
	}
	return nil
}
