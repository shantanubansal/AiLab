// Package tenants is the repository over the tenants table.
//
// v1 keeps it intentionally narrow: lookup by id. Tenants are created out of
// band (seeded for dev, provisioned by WorkOS webhooks in production).
package tenants

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/db"
)

// Tenant is one row of the tenants table.
type Tenant struct {
	ID        string
	Slug      string
	Name      string
	CreatedAt time.Time
}

// Repo gives access to tenants. Construct it with NewRepo.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// Get fetches a tenant by id. Returns db.ErrNotFound when no row matches.
func (r *Repo) Get(ctx context.Context, id string) (Tenant, error) {
	var t Tenant
	err := r.pool.QueryRow(ctx, `
		SELECT id, slug, name, created_at
		FROM tenants
		WHERE id = $1
	`, id).Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, db.ErrNotFound
	}
	if err != nil {
		return Tenant{}, err
	}
	return t, nil
}

// Upsert creates a tenant or updates its slug/name if it already exists.
// Used by the WorkOS organization webhook for organization.created and
// organization.updated.
func (r *Repo) Upsert(ctx context.Context, id, slug, name string) (Tenant, error) {
	var t Tenant
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slug, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET slug = EXCLUDED.slug, name = EXCLUDED.name
		RETURNING id, slug, name, created_at
	`, id, slug, name).Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt)
	return t, err
}

// Delete removes a tenant by id. ON DELETE CASCADE on every child table
// cleans up agents, runs, builds, triggers, secrets, audit_events,
// usage_events. Returns db.ErrNotFound when no row matched.
func (r *Repo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return db.ErrNotFound
	}
	return nil
}
