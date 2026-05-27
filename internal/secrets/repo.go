// Repository for the secrets table. Values are sealed at rest with
// cryptobox (AES-256-GCM under API_SECRETS_KEY). The API only ever
// returns secret names to clients; the plaintext leaves the boundary
// once, at the moment of projection into a k8s Secret owned by a run
// or deployment.
package secrets

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/cryptobox"
	"github.com/shantanubansal/AiLab/internal/db"
)

// Secret is one row of the secrets table. Value is only populated by
// methods that explicitly decrypt — list endpoints leave it empty.
type Secret struct {
	ID        string
	TenantID  string
	Name      string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Repo gives access to tenant-scoped secrets.
type Repo struct {
	pool *db.Pool
	box  *cryptobox.Box
}

// NewRepo wires a Repo to a pgx pool and an at-rest crypto box.
func NewRepo(pool *db.Pool, box *cryptobox.Box) *Repo { return &Repo{pool: pool, box: box} }

// Upsert creates or updates a tenant secret by name. Idempotent: the
// (tenant_id, name) unique constraint is honored via ON CONFLICT.
func (r *Repo) Upsert(ctx context.Context, tenantID, name, value string) (Secret, error) {
	if name == "" {
		return Secret{}, errors.New("secrets: name required")
	}
	ct, err := r.box.Seal([]byte(value))
	if err != nil {
		return Secret{}, err
	}
	var s Secret
	err = r.pool.QueryRow(ctx, `
		INSERT INTO secrets (tenant_id, name, value_ciphertext)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, name)
		DO UPDATE SET value_ciphertext = EXCLUDED.value_ciphertext,
		              updated_at = now()
		RETURNING id, tenant_id, name, created_at, updated_at
	`, tenantID, name, ct).Scan(&s.ID, &s.TenantID, &s.Name, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return Secret{}, err
	}
	return s, nil
}

// List returns metadata for the tenant's secrets — never the value.
func (r *Repo) List(ctx context.Context, tenantID string) ([]Secret, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, name, created_at, updated_at
		FROM secrets
		WHERE tenant_id = $1
		ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		var s Secret
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Delete removes a tenant secret by name.
func (r *Repo) Delete(ctx context.Context, tenantID, name string) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM secrets WHERE tenant_id = $1 AND name = $2
	`, tenantID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return db.ErrNotFound
	}
	return nil
}

// Resolve decrypts the named secrets for a tenant and returns them as a
// map<name>=plaintext. Names not found are silently omitted; the caller
// is responsible for checking that every required name was returned.
func (r *Repo) Resolve(ctx context.Context, tenantID string, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT name, value_ciphertext
		FROM secrets
		WHERE tenant_id = $1 AND name = ANY($2)
	`, tenantID, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(names))
	for rows.Next() {
		var name, ct string
		if err := rows.Scan(&name, &ct); err != nil {
			return nil, err
		}
		plain, err := r.box.Open(ct)
		if err != nil {
			return nil, err
		}
		out[name] = string(plain)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if errors.Is(rows.Err(), pgx.ErrNoRows) {
		return out, nil
	}
	return out, nil
}
