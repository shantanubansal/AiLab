// Append-only audit log. Every state-changing write to the platform
// passes through here. Reads stay out of the log on purpose — volume
// grows linearly with read traffic and the value of that data is low
// vs. the storage / query cost.
//
// The Log() entrypoint takes a context and the identity from it; the
// caller doesn't have to thread tenant/user IDs explicitly.

package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

// Standard action verbs. New entries don't have to match these — the
// column is just text — but using them keeps queries simple.
const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionDelete    = "delete"
	ActionTrigger   = "trigger"
	ActionDeploy    = "deploy"
	ActionUndeploy  = "undeploy"
	ActionRotate    = "rotate"
	ActionInvoke    = "invoke" // webhook receivers
)

// Common resource_type values.
const (
	ResourceAgent      = "agent"
	ResourceRun        = "run"
	ResourceTrigger    = "trigger"
	ResourceBuild      = "build"
	ResourceSecret     = "secret"
	ResourceDeployment = "deployment"
)

// Event is one row of audit_events.
type Event struct {
	ID           int64
	TenantID     string
	UserID       string
	Action       string
	ResourceType string
	ResourceID   string
	Metadata     map[string]any
	RequestID    string
	CreatedAt    time.Time
}

// Repo writes and reads audit_events.
type Repo struct {
	pool *db.Pool
}

// NewRepo wires a Repo to a pgx pool.
func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// global is the process-wide audit repo used by Log(). Set once in
// main() via SetGlobal so handlers don't have to thread a Repo pointer
// through every constructor. Audit writes never block requests.
var global *Repo

// SetGlobal stores the audit repo used by the package-level Log() helper.
func SetGlobal(r *Repo) { global = r }

// Log is the package-level shortcut for the common case of recording one
// event from a handler. Returns silently if SetGlobal hasn't been called.
func Log(ctx context.Context, action, resourceType, resourceID string, metadata map[string]any, requestID string) {
	if global == nil {
		return
	}
	global.Log(ctx, action, resourceType, resourceID, metadata, requestID)
}

// Log appends one event. The (tenant, user) come from auth.FromContext;
// when there is no identity we silently no-op rather than fail the caller —
// audit log failures should never break the underlying mutation.
//
// This is called as a fire-and-forget side effect from handlers. We
// intentionally don't propagate the error up to the HTTP response.
func (r *Repo) Log(ctx context.Context, action, resourceType, resourceID string, metadata map[string]any, requestID string) {
	if r == nil {
		return
	}
	id, ok := auth.FromContext(ctx)
	if !ok {
		return
	}
	var metaJSON []byte
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err == nil {
			metaJSON = b
		}
	}
	// Detach from any short request deadline — the audit insert is
	// independent of the parent request's success/failure.
	bg, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = r.pool.Exec(bg, `
		INSERT INTO audit_events (tenant_id, user_id, action, resource_type, resource_id, metadata, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
	`, id.TenantID, id.UserID, action, resourceType, resourceID, metaJSON, requestID)
}

// List returns recent audit events for the tenant, newest first.
func (r *Repo) List(ctx context.Context, tenantID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, COALESCE(user_id, ''), action, resource_type,
		       COALESCE(resource_id, ''), metadata, COALESCE(request_id, ''),
		       created_at
		FROM audit_events
		WHERE tenant_id = $1
		ORDER BY id DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var meta []byte
		if err := rows.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Action,
			&e.ResourceType, &e.ResourceID, &meta, &e.RequestID, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
