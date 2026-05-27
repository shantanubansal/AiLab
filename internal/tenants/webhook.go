// WorkOS organization webhook. POST /v1/webhooks/workos receives
// organization.created / .updated / .deleted events and reflects them
// into the tenants table.
//
// Signature header format (WorkOS): "t=<unix_seconds>, v1=<hex_hmac>"
// where v1 = HMAC-SHA256(<t>.<rawBody>, signingSecret).

package tenants

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/shantanubansal/AiLab/internal/audit"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

// workosNamespace is a fixed UUIDv4 used to derive deterministic v5
// UUIDs from WorkOS organization ids. The org id stays in tenants.slug
// for humans; tenants.id becomes a stable UUID the platform uses as a
// foreign key everywhere else (k8s namespace suffix, RLS, indexes).
var workosNamespace = uuid.MustParse("8c0e8b3a-3a4f-4f9b-9c1f-3b0c1b6f3a4e")

// tenantIDForOrg maps a WorkOS organization id to a deterministic
// tenant UUID. Same org id → same UUID, so .updated / .deleted line up
// with .created without an extra lookup.
func tenantIDForOrg(orgID string) string {
	return uuid.NewSHA1(workosNamespace, []byte(orgID)).String()
}

// WebhookHandler verifies + dispatches WorkOS organization events.
type WebhookHandler struct {
	Repo          *Repo
	SigningSecret string
	// Tolerance is the maximum allowed clock skew between the WorkOS
	// signature timestamp and now. Defaults to 5 minutes.
	Tolerance time.Duration
}

// Routes mounts POST /v1/webhooks/workos.
func (h *WebhookHandler) Routes(r chi.Router) {
	r.Post("/workos", h.handle)
}

// workosEvent is the subset of the WorkOS webhook payload we read.
type workosEvent struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
}

func (h *WebhookHandler) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := h.verify(r.Header.Get("WorkOS-Signature"), body); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	var ev workosEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	orgID, _ := ev.Data["id"].(string)
	slug, _ := ev.Data["slug"].(string)
	name, _ := ev.Data["name"].(string)
	if orgID == "" {
		http.Error(w, "missing data.id", http.StatusBadRequest)
		return
	}
	tenantID := tenantIDForOrg(orgID)
	if slug == "" {
		// WorkOS slugs are optional; surface the org id so an empty
		// slug doesn't collide on the unique constraint across tenants.
		slug = orgID
	}

	// Synthetic identity so audit attributes the change to the platform
	// integration rather than dropping it.
	ctx := auth.WithIdentity(r.Context(), auth.Identity{TenantID: tenantID, UserID: "workos:" + ev.Event})

	switch ev.Event {
	case "organization.created", "organization.updated":
		if _, err := h.Repo.Upsert(ctx, tenantID, slug, name); err != nil {
			http.Error(w, "upsert: "+err.Error(), http.StatusInternalServerError)
			return
		}
		action := audit.ActionCreate
		if ev.Event == "organization.updated" {
			action = audit.ActionUpdate
		}
		audit.Log(ctx, action, "tenant", tenantID, map[string]any{
			"workosOrgId": orgID,
			"slug":        slug,
			"name":        name,
		}, middleware.GetReqID(r.Context()))

	case "organization.deleted":
		if err := h.Repo.Delete(ctx, tenantID); err != nil && !errors.Is(err, db.ErrNotFound) {
			http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Log(ctx, audit.ActionDelete, "tenant", tenantID, map[string]any{"workosOrgId": orgID}, middleware.GetReqID(r.Context()))

	default:
		// Unknown event types are acknowledged so WorkOS doesn't retry;
		// we just don't act on them.
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *WebhookHandler) verify(sigHeader string, body []byte) error {
	if h.SigningSecret == "" {
		return errors.New("webhook signing secret not configured")
	}
	parts := strings.Split(sigHeader, ",")
	var tsStr, v1 string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch {
		case strings.HasPrefix(p, "t="):
			tsStr = strings.TrimPrefix(p, "t=")
		case strings.HasPrefix(p, "v1="):
			v1 = strings.TrimPrefix(p, "v1=")
		}
	}
	if tsStr == "" || v1 == "" {
		return errors.New("malformed signature header")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("bad timestamp: %w", err)
	}
	tolerance := h.Tolerance
	if tolerance == 0 {
		tolerance = 5 * time.Minute
	}
	if delta := time.Since(time.Unix(ts, 0)); delta > tolerance || delta < -tolerance {
		return fmt.Errorf("signature timestamp outside tolerance: %s", delta)
	}
	want, err := hex.DecodeString(v1)
	if err != nil {
		return fmt.Errorf("bad signature hex: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(h.SigningSecret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	mac.Write(body)
	got := mac.Sum(nil)
	if !hmac.Equal(want, got) {
		return errors.New("signature mismatch")
	}
	return nil
}
