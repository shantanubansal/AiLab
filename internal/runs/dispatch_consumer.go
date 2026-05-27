// DispatchConsumer materializes run.requested events into AgentRun custom
// resources. It is the bridge between the api's "I want this run" intent
// and the reconciler's CRD-driven execution loop.
//
// On message:
//   1. ensure the tenant namespace exists (idempotent)
//   2. create an AgentRun CR in that namespace named with the run id
// The existing reconciler in controller.go takes it from there.

package runs

import (
	"context"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/telemetry"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// DispatchConsumer subscribes to run.requested and projects events into
// AgentRun CRs against the cluster pointed at by Client. Quota / Limit
// give the per-tenant defaults applied the first time a namespace is
// touched.
type DispatchConsumer struct {
	Client client.Client
	Quota  QuotaSpec
	Limit  LimitSpec
}

// Start wires up the JetStream consumer. Returns once the subscription is
// active; messages are handled on the bus's goroutines.
func (d *DispatchConsumer) Start(ctx context.Context, bus *eventbus.Bus) error {
	return bus.Subscribe(ctx, "ailab-controller-run-requested", events.SubjectRunRequested, d.handle)
}

func (d *DispatchConsumer) handle(ctx context.Context, data []byte) error {
	var ev events.RunRequested
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal run.requested: %w", err)
	}
	ctx = telemetry.Extract(ctx, ev.TraceContext)
	ctx, span := otel.Tracer("ailab/controller").Start(ctx, "run.dispatch")
	defer span.End()
	span.SetAttributes(
		attribute.String("ailab.tenant_id", ev.TenantID),
		attribute.String("ailab.run_id", ev.RunID),
	)
	logger := log.FromContext(ctx).WithValues("tenantId", ev.TenantID, "runId", ev.RunID)

	ns := tenantNamespace(ev.TenantID)
	if err := d.ensureNamespace(ctx, ns); err != nil {
		return fmt.Errorf("ensure namespace %s: %w", ns, err)
	}
	if err := EnsureTenantQuotas(ctx, d.Client, ns, d.Quota, d.Limit); err != nil {
		logger.Info("quota apply failed; continuing", "error", err.Error())
	}

	inputsJSON := ""
	if ev.Inputs != nil {
		b, err := json.Marshal(ev.Inputs)
		if err != nil {
			return fmt.Errorf("marshal inputs: %w", err)
		}
		inputsJSON = string(b)
	}

	cr := &AgentRun{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AgentRun",
			APIVersion: GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ev.RunID,
			Namespace: ns,
			Labels: map[string]string{
				"ailab.uipath.com/tenant": ev.TenantID,
				"ailab.uipath.com/agent":  ev.AgentID,
			},
		},
		Spec: AgentRunSpec{
			TenantID:  ev.TenantID,
			AgentName: ev.AgentID,
			Image:     ev.Image,
			Inputs:    inputsJSON,
			TraceID:   ev.TraceID,
			SecretRef: ev.SecretRef,
		},
	}
	if err := d.Client.Create(ctx, cr); err != nil {
		if errors.IsAlreadyExists(err) {
			// At-least-once delivery: the CR already exists. Treat as a
			// successful dispatch so the message is acknowledged.
			logger.Info("agentrun already exists; ack and move on")
			return nil
		}
		return fmt.Errorf("create agentrun: %w", err)
	}
	logger.Info("agentrun created", "namespace", ns)
	return nil
}

func (d *DispatchConsumer) ensureNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := d.Client.Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// tenantNamespace derives the k8s namespace name for a tenant id. UUIDs
// satisfy the DNS-1123 label syntax once we prefix and lowercase.
func tenantNamespace(tenantID string) string {
	return "tenant-" + tenantID
}
