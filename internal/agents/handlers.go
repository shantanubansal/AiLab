// HTTP handlers for /v1/agents. Each handler resolves the caller's tenant
// from the auth.Identity attached by middleware, then delegates to the repo.
package agents

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/pkg/manifest"
)

// Handlers exposes the agent HTTP surface.
type Handlers struct {
	Repo *Repo
}

// Routes mounts handlers on a chi router.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Route("/{agentId}", func(r chi.Router) {
		r.Get("/", h.get)
		r.Delete("/", h.delete)
	})
}

// createRequest mirrors the OpenAPI CreateAgentRequest schema.
type createRequest struct {
	Manifest manifest.Manifest `json:"manifest"`
}

// agentDTO is the JSON we return — kept distinct from the DB row.
type agentDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	Runtime   string    `json:"runtime"`
	Image     *string   `json:"image"`
	CreatedAt time.Time `json:"createdAt"`
}

func toDTO(a Agent) agentDTO {
	return agentDTO{
		ID:        a.ID,
		Name:      a.Name,
		Mode:      a.Mode,
		Runtime:   a.Runtime,
		Image:     a.Image,
		CreatedAt: a.CreatedAt,
	}
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rows, err := h.Repo.List(r.Context(), id.TenantID, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]agentDTO, 0, len(rows))
	for _, a := range rows {
		out = append(out, toDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := req.Manifest.Validate(); err != nil {
		http.Error(w, "invalid manifest: "+err.Error(), http.StatusBadRequest)
		return
	}
	a, err := h.Repo.Create(r.Context(), id.TenantID, req.Manifest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, toDTO(a))
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	a, err := h.Repo.Get(r.Context(), id.TenantID, agentID)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(a))
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	err := h.Repo.Delete(r.Context(), id.TenantID, agentID)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
