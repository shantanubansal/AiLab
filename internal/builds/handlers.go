// HTTP handlers for builds: POST /v1/agents/{agentId}/builds inserts a
// pending build row and publishes build.requested for the builder service.
package builds

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/audit"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/telemetry"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// Handlers exposes the build HTTP surface.
type Handlers struct {
	Builds *Repo
	Agents *agents.Repo
	Bus    *eventbus.Bus
}

// Routes mounts handlers under /v1/agents/{agentId}/builds.
func (h *Handlers) Routes(r chi.Router) {
	r.Post("/", h.create)
}

type createRequest struct {
	SourceURL string `json:"sourceUrl"`
}

type buildDTO struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agentId"`
	Status    string    `json:"status"`
	Image     string    `json:"image,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	if _, err := h.Agents.Get(r.Context(), id.TenantID, agentID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceURL == "" {
		http.Error(w, "bad request: sourceUrl required", http.StatusBadRequest)
		return
	}

	b, err := h.Builds.Create(r.Context(), id.TenantID, agentID, req.SourceURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.Bus.Publish(pubCtx, events.SubjectBuildRequested, events.BuildRequested{
		TenantID:     id.TenantID,
		AgentID:      agentID,
		BuildID:      b.ID,
		SourceURL:    req.SourceURL,
		TraceContext: telemetry.Inject(r.Context()),
		At:           time.Now().UTC(),
	}); err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusBadGateway)
		return
	}
	audit.Log(r.Context(), audit.ActionCreate, audit.ResourceBuild, b.ID, map[string]any{
		"agentId":   agentID,
		"sourceUrl": req.SourceURL,
	}, middleware.GetReqID(r.Context()))

	writeJSON(w, http.StatusAccepted, buildDTO{
		ID:        b.ID,
		AgentID:   b.AgentID,
		Status:    string(b.Status),
		CreatedAt: b.CreatedAt,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
