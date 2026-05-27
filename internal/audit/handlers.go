// HTTP handler for /v1/audit — tenant-scoped list of recent events.
package audit

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/shantanubansal/AiLab/internal/auth"
)

// Handlers exposes the audit list endpoint.
type Handlers struct {
	Repo *Repo
}

// Routes mounts the list endpoint under /v1/audit.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
}

type eventDTO struct {
	ID           int64          `json:"id"`
	UserID       string         `json:"userId,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	RequestID    string         `json:"requestId,omitempty"`
	CreatedAt    string         `json:"createdAt"`
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	events, err := h.Repo.List(r.Context(), id.TenantID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]eventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, eventDTO{
			ID:           e.ID,
			UserID:       e.UserID,
			Action:       e.Action,
			ResourceType: e.ResourceType,
			ResourceID:   e.ResourceID,
			Metadata:     e.Metadata,
			RequestID:    e.RequestID,
			CreatedAt:    e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": out})
}
