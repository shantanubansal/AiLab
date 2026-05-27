// Command builder turns user source into signed OCI images.
//
// Pipeline (each step is a k8s Job in the build namespace):
//   1. Kaniko build + push                       (always)
//   2. Trivy vulnerability scan                  (BUILDER_TRIVY_ENABLED=true)
//   3. cosign sign                                (BUILDER_COSIGN_ENABLED=true)
//
// Failures map to the builds.status check constraint:
//   * Kaniko fail → status=failed
//   * Trivy fail (HIGH/CRITICAL vulns) → status=blocked
//   * cosign fail → status=failed
//
// On success the agent's image pointer is updated to the signed reference,
// transactionally with the builds row, by builds.Repo.MarkCompleted.
//
// Registry config: BUILDER_REGISTRY. For local dev with kind, follow
// https://kind.sigs.k8s.io/docs/user/local-registry/ and set it to
// localhost:5001 (or whatever your local registry exposes).
//
// Cosign signing: BUILDER_COSIGN_SECRET must name a k8s Secret in the
// build namespace with keys "cosign.key" (private key) and optionally
// "cosign.password". v1.5 replaces this with keyless OIDC signing.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/builds"
	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/kube"
	"github.com/shantanubansal/AiLab/pkg/events"
)

func main() {
	cfg := config.LoadAPI()
	bc := buildConfig{
		registry:        envOr("BUILDER_REGISTRY", ""),
		namespace:       envOr("BUILDER_NAMESPACE", "ailab-builds"),
		kanikoImage:     envOr("BUILDER_KANIKO_IMAGE", "gcr.io/kaniko-project/executor:v1.23.2"),
		trivyEnabled:    envBool("BUILDER_TRIVY_ENABLED"),
		trivyImage:      envOr("BUILDER_TRIVY_IMAGE", "aquasec/trivy:latest"),
		trivySeverity:   envOr("BUILDER_TRIVY_SEVERITY", "HIGH,CRITICAL"),
		cosignEnabled:   envBool("BUILDER_COSIGN_ENABLED"),
		cosignImage:     envOr("BUILDER_COSIGN_IMAGE", "gcr.io/projectsigstore/cosign:v2.4.1"),
		cosignSecret:    envOr("BUILDER_COSIGN_SECRET", ""),
		cosignKeyPath:   envOr("BUILDER_COSIGN_KEY_PATH", "/keys/cosign.key"),
		buildTimeout:    30 * time.Minute,
		scanTimeout:     10 * time.Minute,
		signTimeout:     5 * time.Minute,
	}

	if bc.registry == "" {
		log.Println("warning: BUILDER_REGISTRY is empty; builds will be attempted but pushes will fail")
	}
	if bc.cosignEnabled && bc.cosignSecret == "" {
		log.Fatalf("BUILDER_COSIGN_ENABLED=true requires BUILDER_COSIGN_SECRET")
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres open: %v", err)
	}
	defer pool.Close()

	bus, err := eventbus.Connect(rootCtx, cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer bus.Close()

	kubeClient, err := kube.New()
	if err != nil {
		log.Fatalf("kube client: %v", err)
	}

	if err := ensureNamespace(rootCtx, kubeClient, bc.namespace); err != nil {
		log.Fatalf("ensure build namespace: %v", err)
	}

	w := &worker{
		bus:    bus,
		kube:   kubeClient,
		builds: builds.NewRepo(pool),
		agents: agents.NewRepo(pool),
		cfg:    bc,
	}

	if err := bus.Subscribe(rootCtx, "ailab-builder", events.SubjectBuildRequested, w.handle); err != nil {
		log.Fatalf("subscribe build.requested: %v", err)
	}
	log.Printf("builder subscribed (trivy=%v cosign=%v)", bc.trivyEnabled, bc.cosignEnabled)

	<-rootCtx.Done()
}

type buildConfig struct {
	registry      string
	namespace     string
	kanikoImage   string
	trivyEnabled  bool
	trivyImage    string
	trivySeverity string
	cosignEnabled bool
	cosignImage   string
	cosignSecret  string
	cosignKeyPath string
	buildTimeout  time.Duration
	scanTimeout   time.Duration
	signTimeout   time.Duration
}

type worker struct {
	bus    *eventbus.Bus
	kube   kubernetes.Interface
	builds *builds.Repo
	agents *agents.Repo
	cfg    buildConfig
}

func (w *worker) handle(ctx context.Context, data []byte) error {
	var ev events.BuildRequested
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	log.Printf("build %s queued (agent=%s tenant=%s)", ev.BuildID, ev.AgentID, ev.TenantID)

	if err := w.builds.MarkRunning(ctx, ev.BuildID); err != nil {
		log.Printf("mark running: %v", err)
	}

	agent, err := w.agents.Get(ctx, ev.TenantID, ev.AgentID)
	if err != nil {
		_ = w.complete(ctx, ev, builds.StatusFailed, "", fmt.Sprintf("resolve agent: %v", err))
		return nil
	}

	imageRef := fmt.Sprintf("%s/%s/%s:%s", w.cfg.registry, ev.TenantID, agent.Name, ev.BuildID)

	// 1. Kaniko build + push.
	buildJob := buildKanikoJob("build-"+ev.BuildID, w.cfg.namespace, w.cfg.kanikoImage, ev.SourceURL, imageRef)
	if err := w.runJob(ctx, buildJob, w.cfg.buildTimeout); err != nil {
		_ = w.complete(ctx, ev, builds.StatusFailed, "", fmt.Sprintf("kaniko: %v", err))
		return nil
	}

	// 2. Trivy scan. Vuln findings → blocked (not failed).
	if w.cfg.trivyEnabled {
		scanJob := buildTrivyJob("scan-"+ev.BuildID, w.cfg.namespace, w.cfg.trivyImage, w.cfg.trivySeverity, imageRef)
		if err := w.runJob(ctx, scanJob, w.cfg.scanTimeout); err != nil {
			_ = w.complete(ctx, ev, builds.StatusBlocked, "", fmt.Sprintf("trivy: %v", err))
			return nil
		}
	}

	// 3. cosign sign.
	if w.cfg.cosignEnabled {
		signJob := buildCosignJob("sign-"+ev.BuildID, w.cfg.namespace, w.cfg.cosignImage, w.cfg.cosignSecret, w.cfg.cosignKeyPath, imageRef)
		if err := w.runJob(ctx, signJob, w.cfg.signTimeout); err != nil {
			_ = w.complete(ctx, ev, builds.StatusFailed, "", fmt.Sprintf("cosign: %v", err))
			return nil
		}
	}

	_ = w.complete(ctx, ev, builds.StatusSucceeded, imageRef, "")
	return nil
}

// runJob creates a Job and polls until it reaches a terminal phase or the
// timeout elapses. Returns nil on success, otherwise an error with the
// job's last failure message.
func (w *worker) runJob(ctx context.Context, job *batchv1.Job, timeout time.Duration) error {
	if _, err := w.kube.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create %s: %w", job.Name, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		j, err := w.kube.BatchV1().Jobs(job.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if j.Status.Succeeded > 0 {
			return nil
		}
		if j.Status.Failed > 0 {
			return fmt.Errorf("%s failed: %s", job.Name, lastFailureMessage(j))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s timed out", job.Name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (w *worker) complete(ctx context.Context, ev events.BuildRequested, status builds.Status, image, errMsg string) error {
	now := time.Now().UTC()
	if err := w.builds.MarkCompleted(ctx, ev.BuildID, status, image, errMsg, now); err != nil {
		log.Printf("mark completed: %v", err)
	}
	eventStatus := events.BuildStatusFailed
	switch status {
	case builds.StatusSucceeded:
		eventStatus = events.BuildStatusSucceeded
	case builds.StatusBlocked:
		eventStatus = events.BuildStatusBlocked
	}
	return w.bus.Publish(ctx, events.SubjectBuildCompleted, events.BuildCompleted{
		TenantID: ev.TenantID,
		AgentID:  ev.AgentID,
		BuildID:  ev.BuildID,
		Status:   eventStatus,
		Image:    image,
		Error:    errMsg,
		EndedAt:  now,
	})
}

// ---- Job builders ----

func buildKanikoJob(name, namespace, kanikoImage, sourceURL, destination string) *batchv1.Job {
	context := sourceURL
	if strings.HasPrefix(sourceURL, "git+") {
		context = strings.TrimPrefix(sourceURL, "git+")
	}
	return oneShotJob(name, namespace, corev1.Container{
		Name:  "kaniko",
		Image: kanikoImage,
		Args: []string{
			"--context=" + context,
			"--destination=" + destination,
			"--cache=false",
			"--insecure",
			"--skip-tls-verify",
		},
	}, nil, nil)
}

func buildTrivyJob(name, namespace, trivyImage, severity, image string) *batchv1.Job {
	return oneShotJob(name, namespace, corev1.Container{
		Name:  "trivy",
		Image: trivyImage,
		Args: []string{
			"image",
			"--severity", severity,
			"--ignore-unfixed",
			"--exit-code", "1",
			"--no-progress",
			image,
		},
	}, nil, nil)
}

func buildCosignJob(name, namespace, cosignImage, secretName, keyPath, image string) *batchv1.Job {
	const volName = "cosign-key"
	mountDir := strings.TrimSuffix(keyPath, "/"+filenameOf(keyPath))
	volume := corev1.Volume{
		Name: volName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: secretName},
		},
	}
	mount := corev1.VolumeMount{Name: volName, MountPath: mountDir, ReadOnly: true}

	c := corev1.Container{
		Name:         "cosign",
		Image:        cosignImage,
		Args:         []string{"sign", "--yes", "--key", keyPath, image},
		VolumeMounts: []corev1.VolumeMount{mount},
		Env: []corev1.EnvVar{{
			Name: "COSIGN_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "cosign.password",
					Optional:             ptrBool(true),
				},
			},
		}},
	}
	return oneShotJob(name, namespace, c, []corev1.Volume{volume}, nil)
}

func oneShotJob(name, namespace string, container corev1.Container, volumes []corev1.Volume, _ []metav1.OwnerReference) *batchv1.Job {
	one := int32(1)
	never := int32(0)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: batchv1.JobSpec{
			Parallelism:  &one,
			Completions:  &one,
			BackoffLimit: &never,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{container},
					Volumes:       volumes,
				},
			},
		},
	}
}

func lastFailureMessage(j *batchv1.Job) string {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed {
			return c.Message
		}
	}
	return "job failed"
}

func ensureNamespace(ctx context.Context, k kubernetes.Interface, name string) error {
	_, err := k.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "true" || v == "1" || v == "yes"
}

func ptrBool(b bool) *bool { return &b }

func filenameOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
