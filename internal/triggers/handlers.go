// HTTP handlers for triggers:
//   * POST /v1/agents/{agentId}/triggers — create webhook/cron trigger
//   * GET  /v1/agents/{agentId}/triggers — list triggers
//   * POST /v1/agents/{agentId}/webhooks/{name} — unauthenticated, HMAC-verified
//     receiver. On success: validate signature, insert a Run row, publish
//     run.requested. Returns the new run.

package triggers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/runs"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// Handlers exposes the trigger HTTP surface (authenticated and webhook).
type Handlers struct {
	Triggers *Repo
	Agents   *agents.Repo
	Runs     *runs.Repo
	Bus      *eventbus.Bus
}

// AuthRoutes mounts the authenticated CRUD endpoints under
// /v1/agents/{agentId}/triggers.
func (h *Handlers) AuthRoutes(r chi.Router) {
	r.Post("/", h.create)
	r.Get("/", h.list)
}

// PublicRoutes mounts the unauthenticated webhook receiver.
func (h *Handlers) PublicRoutes(r chi.Router) {
	r.Post("/v1/agents/{agentId}/webhooks/{name}", h.webhook)
}

// createRequest matches the JSON body for POST trigger.
type createRequest struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	CronExpr string `json:"cron,omitempty"`
}

type triggerDTO struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agentId"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	CronExpr      string    `json:"cron,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	WebhookSecret string    `json:"webhookSecret,omitempty"` // only returned on Create for webhook kind
}

func toDTO(t Trigger, includeSecret bool) triggerDTO {
	d := triggerDTO{
		ID:        t.ID,
		AgentID:   t.AgentID,
		Kind:      string(t.Kind),
		Name:      t.Name,
		CronExpr:  t.CronExpr,
		CreatedAt: t.CreatedAt,
	}
	if includeSecret {
		d.WebhookSecret = t.WebhookSecret
	}
	return d
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	if _, err := h.Agents.Get(r.Context(), id.TenantID, agentID); err != nil {
		mapErr(w, err, "agent")
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	t, err := h.Triggers.Create(r.Context(), id.TenantID, agentID, Kind(req.Kind), req.Name, req.CronExpr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, toDTO(t, true))
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	if _, err := h.Agents.Get(r.Context(), id.TenantID, agentID); err != nil {
		mapErr(w, err, "agent")
		return
	}
	rows, err := h.Triggers.ListForAgent(r.Context(), id.TenantID, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]triggerDTO, 0, len(rows))
	for _, t := range rows {
		out = append(out, toDTO(t, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": out})
}

// webhook is unauthenticated; HMAC-SHA256 of the body against the trigger's
// stored secret. Header format mirrors GitHub: sha256=<hex>.
func (h *Handlers) webhook(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentId")
	name := chi.URLParam(r, "name")

	trig, secret, err := h.Triggers.FindWebhookForVerification(r.Context(), agentID, name)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !verifySignature(r.Header.Get("X-AiLab-Signature"), body, secret) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	// Resolve agent for its image — same v1-spine constraint as manual trigger.
	a, err := h.Agents.Get(r.Context(), trig.TenantID, agentID)
	if err != nil {
		mapErr(w, err, "agent")
		return
	}
	if a.Runtime != "container" || a.Image == nil || *a.Image == "" {
		http.Error(w, "agent not runnable in v1 spine", http.StatusUnprocessableEntity)
		return
	}

	var inputs map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &inputs) // best-effort: non-JSON body is fine, runs without inputs
	}

	traceID := uuid.NewString()
	run, err := h.Runs.Create(r.Context(), trig.TenantID, a.ID, inputs, traceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.Bus.Publish(pubCtx, events.SubjectRunRequested, events.RunRequested{
		TenantID: trig.TenantID,
		AgentID:  a.ID,
		RunID:    run.ID,
		Image:    *a.Image,
		Inputs:   inputs,
		TraceID:  traceID,
		At:       time.Now().UTC(),
	}); err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"runId": run.ID})
}

// verifySignature implements GitHub-style "sha256=<hex>" comparison.
func verifySignature(header string, body []byte, secret string) bool {
	if header == "" {
		return false
	}
	prefix := "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := mac.Sum(nil)
	return hmac.Equal(want, got) && len(want) > 0 && !bytes.Equal(want, make([]byte, len(want)))
}

func mapErr(w http.ResponseWriter, err error, resource string) {
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, resource+" not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
