// HTTP handlers for /v1/secrets.
//
//   POST   /v1/secrets         { name, value }   -> 201
//   GET    /v1/secrets                           -> { secrets: [{ name, createdAt, updatedAt }] }
//   DELETE /v1/secrets/{name}                    -> 204
//
// Values are write-only: GET never includes them. They surface again at
// run / deployment materialization time, when api projects a k8s Secret.

package secrets

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shantanubansal/AiLab/internal/audit"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

// Handlers exposes the secrets HTTP surface.
type Handlers struct {
	Repo *Repo
}

// Routes mounts handlers under /v1/secrets.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.upsert)
	r.Delete("/{name}", h.del)
}

type secretDTO struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type upsertRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rows, err := h.Repo.List(r.Context(), id.TenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]secretDTO, 0, len(rows))
	for _, s := range rows {
		out = append(out, secretDTO{Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": out})
}

func (h *Handlers) upsert(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "bad request: name and value required", http.StatusBadRequest)
		return
	}
	s, err := h.Repo.Upsert(r.Context(), id.TenantID, req.Name, req.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	audit.Log(r.Context(), audit.ActionUpdate, audit.ResourceSecret, s.Name, nil, middleware.GetReqID(r.Context()))
	writeJSON(w, http.StatusCreated, secretDTO{Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
}

func (h *Handlers) del(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := chi.URLParam(r, "name")
	if err := h.Repo.Delete(r.Context(), id.TenantID, name); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	audit.Log(r.Context(), audit.ActionDelete, audit.ResourceSecret, name, nil, middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
