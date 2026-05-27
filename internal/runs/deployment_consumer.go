// DeploymentDispatchConsumer materializes deployment.requested /
// deployment.stopped events into AgentDeployment CRs (or removes them).
//
// On requested: ensure tenant namespace, then CreateOrUpdate the
// AgentDeployment CR. The reconciler in deployment_controller.go takes it
// from there to produce a Deployment + Service.
//
// On stopped: delete the matching AgentDeployment; the reconciler's owner
// references propagate the deletion to Deployment + Service.

package runs

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// DeploymentDispatchConsumer reacts to deployment.* events on the bus.
type DeploymentDispatchConsumer struct {
	Client client.Client
}

// Start wires JetStream consumers for both subjects.
func (d *DeploymentDispatchConsumer) Start(ctx context.Context, bus *eventbus.Bus) error {
	if err := bus.Subscribe(ctx, "ailab-controller-deployment-requested", events.SubjectDeploymentRequested, d.handleRequested); err != nil {
		return fmt.Errorf("subscribe deployment.requested: %w", err)
	}
	if err := bus.Subscribe(ctx, "ailab-controller-deployment-stopped", events.SubjectDeploymentStopped, d.handleStopped); err != nil {
		return fmt.Errorf("subscribe deployment.stopped: %w", err)
	}
	return nil
}

func (d *DeploymentDispatchConsumer) handleRequested(ctx context.Context, data []byte) error {
	var ev events.DeploymentRequested
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	logger := log.FromContext(ctx).WithValues("agent", ev.AgentID, "tenant", ev.TenantID)

	ns := tenantNamespace(ev.TenantID)
	if err := d.ensureNamespace(ctx, ns); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	desired := &AgentDeployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AgentDeployment",
			APIVersion: GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ev.AgentName,
			Namespace: ns,
			Labels: map[string]string{
				"ailab.uipath.com/tenant": ev.TenantID,
				"ailab.uipath.com/agent":  ev.AgentID,
			},
		},
		Spec: AgentDeploymentSpec{
			TenantID:   ev.TenantID,
			AgentName:  ev.AgentID,
			Image:      ev.Image,
			Port:       ev.Port,
			HealthPath: ev.HealthPath,
			SecretRef:  ev.SecretRef,
		},
	}

	existing := &AgentDeployment{}
	key := client.ObjectKey{Namespace: ns, Name: ev.AgentName}
	if err := d.Client.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := d.Client.Create(ctx, desired); err != nil && !errors.IsAlreadyExists(err) {
				return fmt.Errorf("create agentdeployment: %w", err)
			}
			logger.Info("agentdeployment created", "namespace", ns)
			return nil
		}
		return fmt.Errorf("get agentdeployment: %w", err)
	}

	existing.Spec = desired.Spec
	if err := d.Client.Update(ctx, existing); err != nil {
		return fmt.Errorf("update agentdeployment: %w", err)
	}
	logger.Info("agentdeployment updated", "namespace", ns)
	return nil
}

func (d *DeploymentDispatchConsumer) handleStopped(ctx context.Context, data []byte) error {
	var ev events.DeploymentStopped
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	logger := log.FromContext(ctx).WithValues("agent", ev.AgentID, "tenant", ev.TenantID)

	ns := tenantNamespace(ev.TenantID)
	ad := &AgentDeployment{ObjectMeta: metav1.ObjectMeta{Name: ev.AgentName, Namespace: ns}}
	if err := d.Client.Delete(ctx, ad); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete agentdeployment: %w", err)
	}
	logger.Info("agentdeployment deleted")
	return nil
}

func (d *DeploymentDispatchConsumer) ensureNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := d.Client.Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
