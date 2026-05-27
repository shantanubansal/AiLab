// Reconciler for AgentRun. It is the spine of the platform's job-style
// execution path: consume an AgentRun resource, project it to a k8s Job in
// the tenant namespace, observe the Job's terminal state, publish
// run.started / run.completed for the api to apply back to Postgres.
//
// v1 is intentionally minimal — production hardening (gVisor RuntimeClass,
// NetworkPolicies, Secret CSI volume mounts, output collection from stdout)
// is layered on top of the basic reconcile loop in this file.

package runs

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// AgentRunReconciler reconciles AgentRun resources into k8s Jobs and emits
// run.started / run.completed on phase transitions.
type AgentRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Bus    *eventbus.Bus
}

// Reconcile is the controller-runtime entrypoint.
func (r *AgentRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("agentrun", req.NamespacedName)

	var run AgentRun
	if err := r.Get(ctx, req.NamespacedName, &run); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get agentrun: %w", err)
	}

	// Terminal phases are sticky: nothing left to do once we've recorded
	// Succeeded or Failed (run.completed has already been published).
	if run.Status.Phase == PhaseSucceeded || run.Status.Phase == PhaseFailed {
		return ctrl.Result{}, nil
	}

	job := &batchv1.Job{}
	jobName := jobNameFor(&run)
	err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: jobName}, job)
	switch {
	case errors.IsNotFound(err):
		newJob := r.buildJob(&run, jobName)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, fmt.Errorf("create job: %w", err)
		}
		run.Status.Phase = PhasePending
		run.Status.JobName = jobName
		if err := r.Status().Update(ctx, &run); err != nil {
			return ctrl.Result{}, fmt.Errorf("status pending: %w", err)
		}
		logger.Info("job created", "job", jobName)
		return ctrl.Result{}, nil

	case err != nil:
		return ctrl.Result{}, fmt.Errorf("get job: %w", err)
	}

	prevPhase := run.Status.Phase
	switch {
	case job.Status.Succeeded > 0:
		run.Status.Phase = PhaseSucceeded
		run.Status.EndedAt = job.Status.CompletionTime
		if run.Status.StartedAt == nil && job.Status.StartTime != nil {
			run.Status.StartedAt = job.Status.StartTime
		}
	case job.Status.Failed > 0:
		run.Status.Phase = PhaseFailed
		run.Status.Message = "job failed"
		now := metav1.Now()
		run.Status.EndedAt = &now
		if run.Status.StartedAt == nil && job.Status.StartTime != nil {
			run.Status.StartedAt = job.Status.StartTime
		}
	case job.Status.Active > 0:
		run.Status.Phase = PhaseRunning
		if run.Status.StartedAt == nil {
			t := metav1.Now()
			if job.Status.StartTime != nil {
				t = *job.Status.StartTime
			}
			run.Status.StartedAt = &t
		}
	}

	if err := r.Status().Update(ctx, &run); err != nil {
		return ctrl.Result{}, fmt.Errorf("status update: %w", err)
	}

	r.publishOnTransition(ctx, &run, prevPhase)
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with the manager.
func (r *AgentRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&AgentRun{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *AgentRunReconciler) publishOnTransition(ctx context.Context, run *AgentRun, prev AgentRunPhase) {
	if r.Bus == nil || run.Status.Phase == prev {
		return
	}
	logger := log.FromContext(ctx).WithValues("agentrun", run.Name, "phase", run.Status.Phase)

	switch run.Status.Phase {
	case PhaseRunning:
		at := time.Now().UTC()
		if run.Status.StartedAt != nil {
			at = run.Status.StartedAt.UTC()
		}
		if err := r.Bus.Publish(ctx, events.SubjectRunStarted, events.RunStarted{
			TenantID: run.Spec.TenantID,
			AgentID:  run.Spec.AgentName,
			RunID:    run.Name,
			At:       at,
		}); err != nil {
			logger.Error(err, "publish run.started")
		}

	case PhaseSucceeded, PhaseFailed:
		endedAt := time.Now().UTC()
		if run.Status.EndedAt != nil {
			endedAt = run.Status.EndedAt.UTC()
		}
		startedAt := endedAt
		if run.Status.StartedAt != nil {
			startedAt = run.Status.StartedAt.UTC()
		}
		st := events.RunStatusSucceeded
		var exit int
		if run.Status.Phase == PhaseFailed {
			st = events.RunStatusFailed
			exit = 1
		}
		if run.Status.ExitCode != nil {
			exit = int(*run.Status.ExitCode)
		}
		if err := r.Bus.Publish(ctx, events.SubjectRunCompleted, events.RunCompleted{
			TenantID:  run.Spec.TenantID,
			AgentID:   run.Spec.AgentName,
			RunID:     run.Name,
			Status:    st,
			ExitCode:  exit,
			Error:     run.Status.Message,
			StartedAt: startedAt,
			EndedAt:   endedAt,
		}); err != nil {
			logger.Error(err, "publish run.completed")
		}
	}
}

func (r *AgentRunReconciler) buildJob(run *AgentRun, jobName string) *batchv1.Job {
	env := make([]corev1.EnvVar, 0, len(run.Spec.Env)+2)
	for k, v := range run.Spec.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	if run.Spec.TraceID != "" {
		env = append(env, corev1.EnvVar{Name: "AGENT_TRACE_ID", Value: run.Spec.TraceID})
	}
	if run.Spec.Inputs != "" {
		env = append(env, corev1.EnvVar{Name: "AGENT_INPUTS", Value: run.Spec.Inputs})
	}

	var envFrom []corev1.EnvFromSource
	if run.Spec.SecretRef != "" {
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: run.Spec.SecretRef},
			},
		})
	}

	one := int32(1)
	never := int32(0)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				"ailab.uipath.com/tenant": run.Spec.TenantID,
				"ailab.uipath.com/agent":  run.Spec.AgentName,
				"ailab.uipath.com/run":    run.Name,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: GroupVersion.String(),
				Kind:       "AgentRun",
				Name:       run.Name,
				UID:        run.UID,
				Controller: ptrBool(true),
			}},
		},
		Spec: batchv1.JobSpec{
			Parallelism:  &one,
			Completions:  &one,
			BackoffLimit: &never,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"ailab.uipath.com/run": run.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "agent",
						Image:   run.Spec.Image,
						Env:     env,
						EnvFrom: envFrom,
					}},
				},
			},
		},
	}
}

func jobNameFor(run *AgentRun) string {
	return "run-" + run.Name
}

func ptrBool(b bool) *bool { return &b }
