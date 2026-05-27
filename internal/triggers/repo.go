// Repository over the triggers table. Webhook trigger creation generates a
// fresh 32-byte plaintext secret that is returned to the caller exactly
// once; the row stores only the AES-GCM ciphertext.
package triggers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shantanubansal/AiLab/internal/cryptobox"
	"github.com/shantanubansal/AiLab/internal/db"
)

// Kind enumerates the v1 trigger kinds.
type Kind string

const (
	KindWebhook Kind = "webhook"
	KindCron    Kind = "cron"
)

// Trigger is one row of the triggers table. WebhookSecret is populated only
// at creation time (returned to caller); subsequent reads leave it empty.
type Trigger struct {
	ID             string
	TenantID       string
	AgentID        string
	Kind           Kind
	Name           string
	CronExpr       string
	CreatedAt      time.Time
	WebhookSecret  string // plaintext, only after Create() or when explicitly opened
	secretSealed   string // base64(nonce || ct || tag), persisted
}

// Repo gives access to triggers. Construct with NewRepo.
type Repo struct {
	pool *db.Pool
	box  *cryptobox.Box
}

// NewRepo wires a Repo to a pgx pool and an at-rest crypto box.
func NewRepo(pool *db.Pool, box *cryptobox.Box) *Repo { return &Repo{pool: pool, box: box} }

// Create persists a trigger. For webhook kind, the function generates a
// 32-byte secret, returns it once via Trigger.WebhookSecret, and stores
// only the ciphertext.
func (r *Repo) Create(ctx context.Context, tenantID, agentID string, kind Kind, name, cronExpr string) (Trigger, error) {
	if name == "" {
		return Trigger{}, errors.New("triggers: name required")
	}
	switch kind {
	case KindWebhook:
	case KindCron:
		if cronExpr == "" {
			return Trigger{}, errors.New("triggers: cronExpr required for kind=cron")
		}
	default:
		return Trigger{}, errors.New("triggers: unknown kind")
	}

	var (
		plain  string
		sealed *string
	)
	if kind == KindWebhook {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return Trigger{}, err
		}
		plain = hex.EncodeToString(raw)
		ct, err := r.box.Seal([]byte(plain))
		if err != nil {
			return Trigger{}, err
		}
		sealed = &ct
	}

	var t Trigger
	var cronColumn *string
	if cronExpr != "" {
		cronColumn = &cronExpr
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO triggers (tenant_id, agent_id, kind, name, webhook_secret_ciphertext, cron_expr)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, agent_id, kind, name, COALESCE(cron_expr, ''), created_at
	`, tenantID, agentID, string(kind), name, sealed, cronColumn).
		Scan(&t.ID, &t.TenantID, &t.AgentID, &t.Kind, &t.Name, &t.CronExpr, &t.CreatedAt)
	if err != nil {
		return Trigger{}, err
	}
	t.WebhookSecret = plain
	return t, nil
}

// ListForAgent returns the agent's triggers; webhook secrets are NOT included.
func (r *Repo) ListForAgent(ctx context.Context, tenantID, agentID string) ([]Trigger, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, agent_id, kind, name, COALESCE(cron_expr, ''), created_at
		FROM triggers
		WHERE tenant_id = $1 AND agent_id = $2
		ORDER BY created_at DESC
	`, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trigger
	for rows.Next() {
		var t Trigger
		if err := rows.Scan(&t.ID, &t.TenantID, &t.AgentID, &t.Kind, &t.Name, &t.CronExpr, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindWebhookForVerification looks up a webhook trigger by (agent, name) and
// decrypts the secret for HMAC verification. The agent's tenant id is
// returned alongside so the dispatcher can build the run.requested event.
func (r *Repo) FindWebhookForVerification(ctx context.Context, agentID, name string) (Trigger, string, error) {
	var (
		t      Trigger
		sealed string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, agent_id, kind, name, COALESCE(cron_expr, ''), created_at,
		       COALESCE(webhook_secret_ciphertext, '')
		FROM triggers
		WHERE agent_id = $1 AND name = $2 AND kind = 'webhook'
	`, agentID, name).
		Scan(&t.ID, &t.TenantID, &t.AgentID, &t.Kind, &t.Name, &t.CronExpr, &t.CreatedAt, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Trigger{}, "", db.ErrNotFound
	}
	if err != nil {
		return Trigger{}, "", err
	}
	if sealed == "" {
		return Trigger{}, "", errors.New("webhook secret missing")
	}
	plain, err := r.box.Open(sealed)
	if err != nil {
		return Trigger{}, "", err
	}
	return t, string(plain), nil
}

// ListAllCron returns every cron trigger across tenants. The triggers
// service uses this to populate its in-process scheduler.
func (r *Repo) ListAllCron(ctx context.Context) ([]Trigger, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, agent_id, kind, name, COALESCE(cron_expr, ''), created_at
		FROM triggers
		WHERE kind = 'cron'
		ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trigger
	for rows.Next() {
		var t Trigger
		if err := rows.Scan(&t.ID, &t.TenantID, &t.AgentID, &t.Kind, &t.Name, &t.CronExpr, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
