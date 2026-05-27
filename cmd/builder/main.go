// Command builder turns user source into signed OCI images.
//
// It subscribes to build.requested on NATS, launches a Kaniko Job in the
// build namespace, polls it to completion, and writes the result back to
// the builds table (and the agent's image field on success). Trivy scan
// and cosign sign hooks are wired but no-op in v1 — they'll arrive as
// init/post containers when registry signing infra lands.
//
// Registry config is intentionally minimal: BUILDER_REGISTRY points at a
// reachable image repo and the image tag follows the
// <registry>/<tenant>/<agent>:<buildId> convention. For local dev with
// kind, follow https://kind.sigs.k8s.io/docs/user/local-registry/ to
// expose a localhost:5001 registry, and set BUILDER_REGISTRY=localhost:5001.

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
	registry := envOr("BUILDER_REGISTRY", "")
	buildNS := envOr("BUILDER_NAMESPACE", "ailab-builds")
	kanikoImage := envOr("BUILDER_KANIKO_IMAGE", "gcr.io/kaniko-project/executor:v1.23.2")

	if registry == "" {
		log.Println("warning: BUILDER_REGISTRY is empty; builds will be attempted but pushes will fail")
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

	if err := ensureNamespace(rootCtx, kubeClient, buildNS); err != nil {
		log.Fatalf("ensure build namespace: %v", err)
	}

	w := &worker{
		pool:        pool,
		bus:         bus,
		kube:        kubeClient,
		builds:      builds.NewRepo(pool),
		agents:      agents.NewRepo(pool),
		registry:    registry,
		namespace:   buildNS,
		kanikoImage: kanikoImage,
	}

	if err := bus.Subscribe(rootCtx, "ailab-builder", events.SubjectBuildRequested, w.handle); err != nil {
		log.Fatalf("subscribe build.requested: %v", err)
	}
	log.Println("builder subscribed to build.requested")

	<-rootCtx.Done()
}

type worker struct {
	pool        *db.Pool
	bus         *eventbus.Bus
	kube        kubernetes.Interface
	builds      *builds.Repo
	agents      *agents.Repo
	registry    string
	namespace   string
	kanikoImage string
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

	// Resolve agent for naming the image. If the agent has been deleted in
	// the interim, fail the build rather than producing an orphan image.
	agent, err := w.agents.Get(ctx, ev.TenantID, ev.AgentID)
	if err != nil {
		_ = w.complete(ctx, ev, builds.StatusFailed, "", fmt.Sprintf("resolve agent: %v", err))
		return nil // ack — replaying won't help
	}

	imageRef := fmt.Sprintf("%s/%s/%s:%s", w.registry, ev.TenantID, agent.Name, ev.BuildID)
	jobName := "build-" + ev.BuildID

	job := buildKanikoJob(jobName, w.namespace, w.kanikoImage, ev.SourceURL, imageRef)
	if _, err := w.kube.BatchV1().Jobs(w.namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		_ = w.complete(ctx, ev, builds.StatusFailed, "", fmt.Sprintf("create kaniko job: %v", err))
		return nil
	}

	status, errMsg := w.watch(ctx, jobName)
	finalStatus := builds.StatusFailed
	finalImage := ""
	if status == "succeeded" {
		finalStatus = builds.StatusSucceeded
		finalImage = imageRef
	}
	_ = w.complete(ctx, ev, finalStatus, finalImage, errMsg)
	return nil
}

func (w *worker) complete(ctx context.Context, ev events.BuildRequested, status builds.Status, image, errMsg string) error {
	now := time.Now().UTC()
	if err := w.builds.MarkCompleted(ctx, ev.BuildID, status, image, errMsg, now); err != nil {
		log.Printf("mark completed: %v", err)
	}
	eventStatus := events.BuildStatusFailed
	if status == builds.StatusSucceeded {
		eventStatus = events.BuildStatusSucceeded
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

// watch polls the build Job until it reaches a terminal phase or the
// timeout elapses. Returns one of "succeeded" / "failed" plus a message.
func (w *worker) watch(ctx context.Context, jobName string) (string, string) {
	deadline := time.Now().Add(30 * time.Minute)
	for {
		j, err := w.kube.BatchV1().Jobs(w.namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return "failed", err.Error()
		}
		switch {
		case j.Status.Succeeded > 0:
			return "succeeded", ""
		case j.Status.Failed > 0:
			return "failed", lastFailureMessage(j)
		}
		if time.Now().After(deadline) {
			return "failed", "build timed out"
		}
		select {
		case <-ctx.Done():
			return "failed", ctx.Err().Error()
		case <-time.After(3 * time.Second):
		}
	}
}

func buildKanikoJob(name, namespace, kanikoImage, sourceURL, destination string) *batchv1.Job {
	one := int32(1)
	never := int32(0)
	// Strategy:
	//   * git URLs go straight to Kaniko's --context=git://...
	//   * tarball URLs use --context=<URL>
	context := sourceURL
	if strings.HasPrefix(sourceURL, "git+") {
		context = strings.TrimPrefix(sourceURL, "git+")
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: batchv1.JobSpec{
			Parallelism:  &one,
			Completions:  &one,
			BackoffLimit: &never,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "kaniko",
						Image: kanikoImage,
						Args: []string{
							"--context=" + context,
							"--destination=" + destination,
							"--cache=false",
							"--insecure",
							"--skip-tls-verify",
						},
					}},
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
