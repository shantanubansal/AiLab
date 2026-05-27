// Deploy / undeploy endpoints for mode=server agents.
//
// POST   /v1/agents/{id}/deploy   publishes deployment.requested
// DELETE /v1/agents/{id}/deploy   publishes deployment.stopped
//
// The controller's dispatch consumer materializes / removes the
// AgentDeployment CR, and its reconciler in turn produces or deletes
// the in-cluster Deployment + Service.

package agents

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"k8s.io/client-go/kubernetes"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/secrets"
	"github.com/shantanubansal/AiLab/pkg/events"
	"github.com/shantanubansal/AiLab/pkg/manifest"
)

// DeployHandlers wires deploy/undeploy onto an existing chi router.
type DeployHandlers struct {
	Repo    *Repo
	Bus     *eventbus.Bus
	K8s     kubernetes.Interface
	Secrets *secrets.Repo
}

// Routes mounts handlers under /v1/agents/{agentId}/deploy.
func (h *DeployHandlers) Routes(r chi.Router) {
	r.Post("/", h.deploy)
	r.Delete("/", h.undeploy)
}

func (h *DeployHandlers) deploy(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	a, err := h.Repo.Get(r.Context(), id.TenantID, agentID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Mode != string(manifest.ModeServer) {
		http.Error(w, "deploy only valid for mode=server", http.StatusUnprocessableEntity)
		return
	}
	if a.Image == nil || *a.Image == "" {
		http.Error(w, "agent has no image to deploy", http.StatusUnprocessableEntity)
		return
	}

	port := int32(8080)
	healthPath := ""
	if a.Manifest.Server != nil {
		if a.Manifest.Server.Port > 0 {
			port = int32(a.Manifest.Server.Port)
		}
		healthPath = a.Manifest.Server.HealthPath
	}

	secretRef, err := h.projectSecrets(r.Context(), a.TenantID, a.Name, a.Manifest.Secrets)
	if err != nil {
		http.Error(w, "secrets projection: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.Bus.Publish(pubCtx, events.SubjectDeploymentRequested, events.DeploymentRequested{
		TenantID:   a.TenantID,
		AgentID:    a.ID,
		AgentName:  a.Name,
		Image:      *a.Image,
		Port:       port,
		HealthPath: healthPath,
		SecretRef:  secretRef,
		At:         time.Now().UTC(),
	}); err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// projectSecrets resolves manifest.secrets and creates the per-agent
// k8s Secret in tenant-<id>. Returns the secret name or "" if there's
// nothing to project.
func (h *DeployHandlers) projectSecrets(ctx context.Context, tenantID, agentName string, names []string) (string, error) {
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
	return mat.ProjectForAgent(ctx, tenantID, agentName, data)
}

func (h *DeployHandlers) undeploy(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	agentID := chi.URLParam(r, "agentId")
	a, err := h.Repo.Get(r.Context(), id.TenantID, agentID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.Bus.Publish(pubCtx, events.SubjectDeploymentStopped, events.DeploymentStopped{
		TenantID:  a.TenantID,
		AgentID:   a.ID,
		AgentName: a.Name,
		At:        time.Now().UTC(),
	}); err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
