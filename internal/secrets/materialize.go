// Projects tenant-scoped secrets into a k8s Secret in the tenant
// namespace just-in-time before a run or deployment starts. This is the
// only path that exposes plaintext outside the api process.
//
// We don't set OwnerReferences here — the run-Secret is owned by its
// AgentRun CR (set by the dispatch consumer once the CR exists), so
// deleting the CR garbage-collects the Secret.

package secrets

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Materializer creates / updates k8s Secrets in a tenant namespace.
type Materializer struct {
	K8s kubernetes.Interface
}

// EnsureTenantNamespace creates tenant-<id> if it doesn't exist. The
// reconciler also bootstraps the namespace on run.requested; doing it
// here too means secret materialization can run ahead of the controller.
func (m *Materializer) EnsureTenantNamespace(ctx context.Context, tenantID string) error {
	ns := tenantNS(tenantID)
	_, err := m.K8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("ensure namespace %s: %w", ns, err)
	}
	return nil
}

// ProjectForRun creates a Secret named "run-<runId>" in the tenant ns,
// containing the resolved name→value pairs. Idempotent: if it already
// exists, the data is replaced.
func (m *Materializer) ProjectForRun(ctx context.Context, tenantID, runID string, data map[string]string) (string, error) {
	name := "run-" + runID
	return name, m.upsertSecret(ctx, tenantNS(tenantID), name, data, map[string]string{
		"ailab.uipath.com/tenant": tenantID,
		"ailab.uipath.com/run":    runID,
	})
}

// ProjectForAgent creates a Secret named "agent-<agentName>-secrets"
// for a long-running deployment. Same idempotent semantics.
func (m *Materializer) ProjectForAgent(ctx context.Context, tenantID, agentName string, data map[string]string) (string, error) {
	name := "agent-" + agentName + "-secrets"
	return name, m.upsertSecret(ctx, tenantNS(tenantID), name, data, map[string]string{
		"ailab.uipath.com/tenant": tenantID,
		"ailab.uipath.com/agent":  agentName,
	})
}

func (m *Materializer) upsertSecret(ctx context.Context, ns, name string, data, labels map[string]string) error {
	bytes := make(map[string][]byte, len(data))
	for k, v := range data {
		bytes[k] = []byte(v)
	}
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Type:       corev1.SecretTypeOpaque,
		Data:       bytes,
	}
	_, err := m.K8s.CoreV1().Secrets(ns).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create secret %s: %w", name, err)
	}
	live, err := m.K8s.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get existing secret %s: %w", name, err)
	}
	live.Data = bytes
	if live.Labels == nil {
		live.Labels = map[string]string{}
	}
	for k, v := range labels {
		live.Labels[k] = v
	}
	if _, err := m.K8s.CoreV1().Secrets(ns).Update(ctx, live, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update secret %s: %w", name, err)
	}
	return nil
}

func tenantNS(tenantID string) string { return "tenant-" + tenantID }
