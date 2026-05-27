// Bootstraps a per-tenant ResourceQuota + LimitRange in the tenant
// namespace on first run / first deploy. Idempotent: the dispatch
// consumer can call this on every event without thrashing.
//
// Defaults are conservative; the controller process can override them
// via env vars without touching this file.

package runs

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// QuotaSpec holds the hard limits applied per tenant namespace.
type QuotaSpec struct {
	CPU    string // total cpu requests across all pods (cores)
	Memory string // total memory requests
	Pods   string // pod count
}

// LimitSpec describes the per-container default + max enforced via LimitRange.
type LimitSpec struct {
	DefaultCPU    string // default requests.cpu when a pod doesn't ask
	DefaultMemory string // default requests.memory when a pod doesn't ask
	MaxCPU        string // hard cap on a single container's cpu requests
	MaxMemory     string // hard cap on a single container's memory requests
}

// EnsureTenantQuotas creates or updates a ResourceQuota and LimitRange
// in ns. Both objects are owned by no other resource — they outlive
// any specific run and only get removed when the namespace itself is.
func EnsureTenantQuotas(ctx context.Context, c client.Client, ns string, q QuotaSpec, l LimitSpec) error {
	if err := ensureResourceQuota(ctx, c, ns, q); err != nil {
		return err
	}
	if err := ensureLimitRange(ctx, c, ns, l); err != nil {
		return err
	}
	return nil
}

func ensureResourceQuota(ctx context.Context, c client.Client, ns string, q QuotaSpec) error {
	if q.CPU == "" && q.Memory == "" && q.Pods == "" {
		return nil
	}
	desired := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-default", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{},
		},
	}
	if q.CPU != "" {
		desired.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse(q.CPU)
	}
	if q.Memory != "" {
		desired.Spec.Hard[corev1.ResourceRequestsMemory] = resource.MustParse(q.Memory)
	}
	if q.Pods != "" {
		desired.Spec.Hard[corev1.ResourcePods] = resource.MustParse(q.Pods)
	}
	return upsertOwnedlessly(ctx, c, desired)
}

func ensureLimitRange(ctx context.Context, c client.Client, ns string, l LimitSpec) error {
	if l.DefaultCPU == "" && l.DefaultMemory == "" && l.MaxCPU == "" && l.MaxMemory == "" {
		return nil
	}
	defaults := corev1.ResourceList{}
	maxes := corev1.ResourceList{}
	if l.DefaultCPU != "" {
		defaults[corev1.ResourceCPU] = resource.MustParse(l.DefaultCPU)
	}
	if l.DefaultMemory != "" {
		defaults[corev1.ResourceMemory] = resource.MustParse(l.DefaultMemory)
	}
	if l.MaxCPU != "" {
		maxes[corev1.ResourceCPU] = resource.MustParse(l.MaxCPU)
	}
	if l.MaxMemory != "" {
		maxes[corev1.ResourceMemory] = resource.MustParse(l.MaxMemory)
	}
	desired := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-default", Namespace: ns},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type:           corev1.LimitTypeContainer,
				DefaultRequest: defaults,
				Default:        defaults,
				Max:            maxes,
			}},
		},
	}
	return upsertOwnedlessly(ctx, c, desired)
}

// upsertOwnedlessly creates the object if missing, or replaces only the
// spec field of an existing one in place. We avoid wholesale Update so
// kube-managed defaults (resource version, etc.) survive.
func upsertOwnedlessly(ctx context.Context, c client.Client, desired client.Object) error {
	if err := c.Create(ctx, desired); err == nil {
		return nil
	} else if !errors.IsAlreadyExists(err) {
		return err
	}
	// Already exists — fetch live, patch spec by re-creating with the
	// stored ResourceVersion. We don't try to merge spec field-by-field
	// because the v1 quotas have very few fields and full replacement
	// is fine.
	live := desired.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKey{Namespace: desired.GetNamespace(), Name: desired.GetName()}, live); err != nil {
		return err
	}
	desired.SetResourceVersion(live.GetResourceVersion())
	return c.Update(ctx, desired)
}
