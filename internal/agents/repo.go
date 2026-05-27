// Repository over the agents table. All reads/writes are scoped to a tenant
// id — there is no cross-tenant query path. The handler layer (in this
// package) is responsible for plumbing the authenticated tenant id into every call.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/pkg/manifest"
)

// Agent is one row of the agents table.
type Agent struct {
	ID        string
	TenantID  string
	Name      string
	Mode      string
	Runtime   string
	Image     *string
	Manifest  manifest.Manifest
	CreatedAt time.Time
}

// Repo gives access to agents. Construct with NewRepo.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// Create inserts an agent row from a validated manifest. The (tenantId, name)
// pair is unique; a conflict surfaces as a Postgres error caller can inspect.
func (r *Repo) Create(ctx context.Context, tenantID string, m manifest.Manifest) (Agent, error) {
	mBytes, err := json.Marshal(m)
	if err != nil {
		return Agent{}, err
	}
	var a Agent
	var imagePtr *string
	if m.Image != "" {
		imagePtr = &m.Image
	}
	err = r.pool.QueryRow(ctx, `
		INSERT INTO agents (tenant_id, name, mode, runtime, image, manifest)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, name, mode, runtime, image, created_at
	`, tenantID, m.Name, string(m.Mode), string(m.Runtime), imagePtr, mBytes).
		Scan(&a.ID, &a.TenantID, &a.Name, &a.Mode, &a.Runtime, &a.Image, &a.CreatedAt)
	if err != nil {
		return Agent{}, err
	}
	a.Manifest = m
	return a, nil
}

// Get fetches an agent by id, scoped to the given tenant. Cross-tenant reads
// are not possible through this method by design — the WHERE clause forbids it.
func (r *Repo) Get(ctx context.Context, tenantID, id string) (Agent, error) {
	var a Agent
	var mBytes []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, mode, runtime, image, manifest, created_at
		FROM agents
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Name, &a.Mode, &a.Runtime, &a.Image, &mBytes, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, db.ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if err := json.Unmarshal(mBytes, &a.Manifest); err != nil {
		return Agent{}, err
	}
	return a, nil
}

// List returns agents in the tenant, newest first. Pagination arrives in v1.1.
func (r *Repo) List(ctx context.Context, tenantID string, limit int) ([]Agent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, name, mode, runtime, image, manifest, created_at
		FROM agents
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var a Agent
		var mBytes []byte
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.Mode, &a.Runtime, &a.Image, &mBytes, &a.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(mBytes, &a.Manifest); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Delete removes an agent. ON DELETE CASCADE on runs cleans those up.
func (r *Repo) Delete(ctx context.Context, tenantID, id string) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM agents WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return db.ErrNotFound
	}
	return nil
}
