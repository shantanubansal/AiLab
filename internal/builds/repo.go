// Repository for the builds table. A build represents one attempt to turn
// a source URL into a signed OCI image referenced by the agent. The
// builder service is the only writer for terminal state; the api writes
// only the initial pending row.
package builds

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/db"
)

// Status mirrors the builds.status check constraint.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusBlocked   Status = "blocked"
)

// Build is one row of the builds table.
type Build struct {
	ID        string
	TenantID  string
	AgentID   string
	Status    Status
	SourceURL string
	Image     string
	Error     string
	CreatedAt time.Time
	EndedAt   *time.Time
}

// Repo wraps builds CRUD. Construct with NewRepo.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// Create inserts a pending build row.
func (r *Repo) Create(ctx context.Context, tenantID, agentID, sourceURL string) (Build, error) {
	var b Build
	err := r.pool.QueryRow(ctx, `
		INSERT INTO builds (tenant_id, agent_id, status, source_url)
		VALUES ($1, $2, 'pending', $3)
		RETURNING id, tenant_id, agent_id, status, source_url, created_at
	`, tenantID, agentID, sourceURL).
		Scan(&b.ID, &b.TenantID, &b.AgentID, &b.Status, &b.SourceURL, &b.CreatedAt)
	return b, err
}

// Get fetches a build by id, scoped to tenant.
func (r *Repo) Get(ctx context.Context, tenantID, id string) (Build, error) {
	var b Build
	err := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, agent_id, status, source_url, COALESCE(image, ''), COALESCE(error, ''), created_at, ended_at
		FROM builds
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).
		Scan(&b.ID, &b.TenantID, &b.AgentID, &b.Status, &b.SourceURL, &b.Image, &b.Error, &b.CreatedAt, &b.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Build{}, db.ErrNotFound
	}
	return b, err
}

// MarkRunning transitions a pending build to running.
func (r *Repo) MarkRunning(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE builds SET status = 'running' WHERE id = $1 AND status = 'pending'
	`, id)
	return err
}

// MarkCompleted writes the terminal state. On success it also updates the
// agent's image field, which is the indirection the runtime path uses.
func (r *Repo) MarkCompleted(ctx context.Context, id string, status Status, image, errMsg string, endedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var agentID, tenantID string
	if err := tx.QueryRow(ctx, `
		UPDATE builds
		SET status = $2,
		    image = NULLIF($3, ''),
		    error = NULLIF($4, ''),
		    ended_at = $5
		WHERE id = $1 AND status NOT IN ('succeeded','failed','blocked')
		RETURNING agent_id, tenant_id
	`, id, string(status), image, errMsg, endedAt).Scan(&agentID, &tenantID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already terminal; nothing to do.
			return tx.Commit(ctx)
		}
		return err
	}

	if status == StatusSucceeded && image != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE agents SET image = $2 WHERE id = $1 AND tenant_id = $3
		`, agentID, image, tenantID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
