// /v1/me — returns the authenticated caller's tenant + user info. The UI
// loads this on every page mount to decide what to show in the header
// and to populate empty states. The seatCount is hardcoded at 1 in v1
// because we don't yet model seats; v1.5 wires WorkOS Directory Sync
// and replaces the constant with a real count.

package tenants

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

// MeHandler serves /v1/me.
type MeHandler struct {
	Repo *Repo
}

// Routes mounts GET /v1/me onto an existing chi router.
func (h *MeHandler) Routes(r chi.Router) {
	r.Get("/", h.me)
}

type meDTO struct {
	UserID    string    `json:"userId"`
	SeatCount int       `json:"seatCount"`
	Tenant    tenantDTO `json:"tenant"`
}

type tenantDTO struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h *MeHandler) me(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := h.Repo.Get(r.Context(), id.TenantID)
	if errors.Is(err, db.ErrNotFound) {
		// Dev tokens that reference an unprovisioned tenant id shouldn't
		// 404 — return a synthetic shell so the UI doesn't break.
		t = Tenant{ID: id.TenantID, Slug: "", Name: "(unprovisioned)", CreatedAt: time.Time{}}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := meDTO{
		UserID:    id.UserID,
		SeatCount: 1, // TODO v1.5: count from WorkOS Directory Sync
		Tenant: tenantDTO{
			ID:        t.ID,
			Slug:      t.Slug,
			Name:      t.Name,
			CreatedAt: t.CreatedAt,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
