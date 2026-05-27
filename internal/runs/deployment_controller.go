// Reconciler for AgentDeployment. Projects long-running agents (mode=server,
// typically MCP) into a k8s Deployment + Service in the tenant namespace.
// The Service name matches AgentDeployment.Name (= sanitized agent name),
// which is how the gateway routes <agent>.<tenant>.<domain> traffic.
//
// v1 stays minimal: single replica, no autoscaling, no scale-to-zero. The
// gateway can layer scale-to-zero on top later by watching Service traffic.

package runs

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AgentDeploymentReconciler reconciles AgentDeployment into Deployment + Service.
type AgentDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager registers the reconciler with the manager.
func (r *AgentDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&AgentDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

// Reconcile is the controller-runtime entrypoint.
func (r *AgentDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("agentdeployment", req.NamespacedName)

	var ad AgentDeployment
	if err := r.Get(ctx, req.NamespacedName, &ad); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get agentdeployment: %w", err)
	}

	if err := r.applyDeployment(ctx, &ad); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply deployment: %w", err)
	}
	if err := r.applyService(ctx, &ad); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply service: %w", err)
	}

	live := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ad.Namespace, Name: ad.Name}, live); err == nil {
		ad.Status.ReadyReplicas = live.Status.ReadyReplicas
	}
	port := portOrDefault(ad.Spec.Port)
	ad.Status.URL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", ad.Name, ad.Namespace, port)
	if err := r.Status().Update(ctx, &ad); err != nil {
		return ctrl.Result{}, fmt.Errorf("status update: %w", err)
	}

	logger.Info("reconciled", "ready", ad.Status.ReadyReplicas, "url", ad.Status.URL)
	return ctrl.Result{}, nil
}

// applyDeployment uses controllerutil.CreateOrUpdate with an in-place mutate
// so we only overwrite fields we manage. K8s-filled defaults survive across
// reconciles, which avoids the spec-thrash → ReplicaSet-churn loop.
func (r *AgentDeploymentReconciler) applyDeployment(ctx context.Context, ad *AgentDeployment) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: ad.Name, Namespace: ad.Namespace},
	}
	port := portOrDefault(ad.Spec.Port)
	labels := agentLabels(ad)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		mergeLabels(&dep.ObjectMeta, labels)

		one := int32(1)
		dep.Spec.Replicas = &one
		// Selector is immutable post-create — only set on first create.
		if dep.Spec.Selector == nil {
			dep.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"ailab.uipath.com/deployment": ad.Name},
			}
		}
		mergeMap(&dep.Spec.Template.Labels, labels)

		if len(dep.Spec.Template.Spec.Containers) == 0 {
			dep.Spec.Template.Spec.Containers = []corev1.Container{{Name: "agent"}}
		}
		c := &dep.Spec.Template.Spec.Containers[0]
		c.Name = "agent"
		c.Image = ad.Spec.Image
		c.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: port, Protocol: corev1.ProtocolTCP}}
		c.Env = envFromMap(ad.Spec.Env)
		c.EnvFrom = nil
		if ad.Spec.SecretRef != "" {
			c.EnvFrom = []corev1.EnvFromSource{{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ad.Spec.SecretRef},
				},
			}}
		}
		c.ReadinessProbe = readinessProbe(ad.Spec.HealthPath, port)

		return controllerutil.SetControllerReference(ad, dep, r.Scheme)
	})
	return err
}

func (r *AgentDeploymentReconciler) applyService(ctx context.Context, ad *AgentDeployment) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: ad.Name, Namespace: ad.Namespace},
	}
	port := portOrDefault(ad.Spec.Port)
	labels := agentLabels(ad)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		mergeLabels(&svc.ObjectMeta, labels)
		// Selector + Ports replaced wholesale; both safe to update.
		svc.Spec.Selector = map[string]string{"ailab.uipath.com/deployment": ad.Name}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       port,
			TargetPort: intstr.FromInt32(port),
			Protocol:   corev1.ProtocolTCP,
		}}
		if svc.Spec.Type == "" {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
		return controllerutil.SetControllerReference(ad, svc, r.Scheme)
	})
	return err
}

func agentLabels(ad *AgentDeployment) map[string]string {
	return map[string]string{
		"ailab.uipath.com/tenant":     ad.Spec.TenantID,
		"ailab.uipath.com/agent":      ad.Spec.AgentName,
		"ailab.uipath.com/deployment": ad.Name,
	}
}

func mergeLabels(m *metav1.ObjectMeta, in map[string]string) {
	if m.Labels == nil {
		m.Labels = make(map[string]string, len(in))
	}
	for k, v := range in {
		m.Labels[k] = v
	}
}

func mergeMap(dst *map[string]string, in map[string]string) {
	if *dst == nil {
		*dst = make(map[string]string, len(in))
	}
	for k, v := range in {
		(*dst)[k] = v
	}
}

func envFromMap(in map[string]string) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(in))
	for k, v := range in {
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

func readinessProbe(path string, port int32) *corev1.Probe {
	if path == "" {
		return nil
	}
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port)},
		},
		PeriodSeconds:       10,
		TimeoutSeconds:      3,
		FailureThreshold:    3,
		InitialDelaySeconds: 2,
	}
}

func portOrDefault(p int32) int32 {
	if p == 0 {
		return 8080
	}
	return p
}
