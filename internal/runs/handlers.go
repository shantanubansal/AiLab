// HTTP handlers for /v1/agents/{id}/runs and /v1/runs/{id}.
//
// POST run: validates the parent agent exists for the tenant, inserts a
// pending row, publishes run.requested on NATS so the controller can pick
// it up, and returns the new run to the caller. The controller writes
// terminal state back via the run.completed handler in cmd/api/main.go.

package runs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/go-chi/chi/v5/middleware"
	"k8s.io/client-go/kubernetes"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/audit"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/loki"
	"github.com/shantanubansal/AiLab/internal/secrets"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// Handlers exposes the run HTTP surface.
type Handlers struct {
	Runs    *Repo
	Agents  *agents.Repo
	Bus     *eventbus.Bus
	K8s     kubernetes.Interface
	Secrets *secrets.Repo
	Loki    *loki.Client
}

// Routes mounts run handlers on a chi router rooted at /v1.
func (h *Handlers) Routes(r chi.Router) {
	r.Route("/agents/{agentId}/runs", func(r chi.Router) {
		r.Get("/", h.listForAgent)
		r.Post("/", h.trigger)
	})
	r.Route("/runs/{runId}", func(r chi.Router) {
		r.Get("/", h.get)
		r.Get("/logs", h.logs)
	})
}

// runDTO is the JSON we return — kept distinct from the DB row.
type runDTO struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agentId"`
	Status    string         `json:"status"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Outputs   map[string]any `json:"outputs,omitempty"`
	ExitCode  *int           `json:"exitCode,omitempty"`
	TraceID   string         `json:"traceId,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	StartedAt *time.Time     `json:"startedAt,omitempty"`
	EndedAt   *time.Time     `json:"endedAt,omitempty"`
}

func toDTO(r Run) runDTO {
	return runDTO{
		ID:        r.ID,
		AgentID:   r.AgentID,
		Status:    string(r.Status),
		Inputs:    r.Inputs,
		Outputs:   r.Outputs,
		ExitCode:  r.ExitCode,
		TraceID:   r.TraceID,
		CreatedAt: r.CreatedAt,
		StartedAt: r.StartedAt,
		EndedAt:   r.EndedAt,
	}
}

func (h *Handlers) listForAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	if _, err := h.Agents.Get(r.Context(), id.TenantID, agentID); err != nil {
		mapAgentErr(w, err)
		return
	}
	rows, err := h.Runs.ListForAgent(r.Context(), id.TenantID, agentID, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]runDTO, 0, len(rows))
	for _, run := range rows {
		out = append(out, toDTO(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// triggerRequest matches the POST body in openapi.yaml.
type triggerRequest struct {
	Inputs map[string]any `json:"inputs,omitempty"`
}

func (h *Handlers) trigger(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer("ailab/api").Start(r.Context(), "run.trigger")
	defer span.End()
	r = r.WithContext(ctx)

	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	span.SetAttributes(
		attribute.String("ailab.tenant_id", id.TenantID),
		attribute.String("ailab.agent_id", agentID),
	)
	agent, err := h.Agents.Get(r.Context(), id.TenantID, agentID)
	if err != nil {
		mapAgentErr(w, err)
		return
	}

	// Only container-runtime agents are runnable in the v1 spine; code agents
	// require the builder to have produced an image first.
	if agent.Runtime != "container" || agent.Image == nil || *agent.Image == "" {
		http.Error(w,
			"agent is not runnable: v1 spine requires runtime=container with image set",
			http.StatusUnprocessableEntity)
		return
	}

	var req triggerRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Use the active span's trace id when present so the platform's
	// AGENT_TRACE_ID surfaces in Tempo alongside the api → controller spans.
	traceID := uuid.NewString()
	if sc := span.SpanContext(); sc.IsValid() {
		traceID = sc.TraceID().String()
	}
	span.SetAttributes(attribute.String("ailab.trace_id", traceID))

	run, err := h.Runs.Create(r.Context(), id.TenantID, agent.ID, req.Inputs, traceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Project the agent's manifest.secrets into a per-run k8s Secret in
	// the tenant namespace. Empty list → no Secret, no SecretRef.
	secretRef, err := h.projectSecrets(r.Context(), id.TenantID, agent.Manifest.Secrets, "run", run.ID)
	if err != nil {
		http.Error(w, "secrets projection: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Publish to the bus. If publish fails after the row is written, the
	// run sits in 'pending' — surfacing as an obvious stuck state in the UI.
	// A janitor job (out of v1 spine scope) can republish those.
	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.Bus.Publish(pubCtx, events.SubjectRunRequested, events.RunRequested{
		TenantID:  id.TenantID,
		AgentID:   agent.ID,
		RunID:     run.ID,
		Image:     *agent.Image,
		Inputs:    req.Inputs,
		TraceID:   traceID,
		SecretRef: secretRef,
		At:        time.Now().UTC(),
	}); err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusBadGateway)
		return
	}

	audit.Log(r.Context(), audit.ActionTrigger, audit.ResourceRun, run.ID, map[string]any{
		"agentId": agent.ID,
		"source":  "manual",
	}, middleware.GetReqID(r.Context()))
	span.SetAttributes(attribute.String("ailab.run_id", run.ID))
	writeJSON(w, http.StatusAccepted, toDTO(run))
}

// projectSecrets resolves manifest.secrets from the secrets repo, materializes
// a k8s Secret, and returns its name. Returns "" when there are no secrets
// to project or when the api lacks a k8s client / secrets repo.
func (h *Handlers) projectSecrets(ctx context.Context, tenantID string, names []string, kind, id string) (string, error) {
	if len(names) == 0 || h.K8s == nil || h.Secrets == nil {
		return "", nil
	}
	data, err := h.Secrets.Resolve(ctx, tenantID, names)
	if err != nil {
		return "", err
	}
	mat := &secrets.Materializer{K8s: h.K8s}
	if err := mat.EnsureTenantNamespace(ctx, tenantID); err != nil {
		return "", err
	}
	switch kind {
	case "run":
		return mat.ProjectForRun(ctx, tenantID, id, data)
	case "agent":
		return mat.ProjectForAgent(ctx, tenantID, id, data)
	}
	return "", nil
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	runID := chi.URLParam(r, "runId")
	run, err := h.Runs.Get(r.Context(), id.TenantID, runID)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(run))
}

func (h *Handlers) logs(w http.ResponseWriter, r *http.Request) {
	h.streamLogs(w, r)
}

func mapAgentErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
