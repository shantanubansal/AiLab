// Command janitor periodically reclaims platform-owned state that no
// owner is going to clean up:
//
//   * k8s Jobs older than JANITOR_JOB_TTL (default 24h) in tenant-* namespaces
//   * "run-<uuid>" Secrets whose corresponding runs row no longer exists
//   * builds stuck in pending/running for > JANITOR_BUILD_STUCK_TTL
//   * (best effort) usage_events older than JANITOR_USAGE_TTL after they've
//     been shipped past by the billing checkpoint
//
// Each tick is best-effort and idempotent. Failures are logged, never fatal.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/kube"
)

func main() {
	cfg := config.LoadAPI()
	jobTTL := envDuration("JANITOR_JOB_TTL", 24*time.Hour)
	buildStuck := envDuration("JANITOR_BUILD_STUCK_TTL", 6*time.Hour)
	usageTTL := envDuration("JANITOR_USAGE_TTL", 90*24*time.Hour)
	interval := envDuration("JANITOR_INTERVAL", 30*time.Minute)

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres open: %v", err)
	}
	defer pool.Close()

	k8sClient, err := kube.New()
	if err != nil {
		log.Fatalf("kube client: %v", err)
	}

	j := &janitor{
		pool:       pool.Pool,
		kube:       k8sClient,
		jobTTL:     jobTTL,
		buildStuck: buildStuck,
		usageTTL:   usageTTL,
	}

	log.Printf("janitor running (interval=%s jobTTL=%s buildStuck=%s usageTTL=%s)",
		interval, jobTTL, buildStuck, usageTTL)
	j.tick(rootCtx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-rootCtx.Done():
			return
		case <-t.C:
			j.tick(rootCtx)
		}
	}
}

type janitor struct {
	pool       *pgxpool.Pool
	kube       kubernetes.Interface
	jobTTL     time.Duration
	buildStuck time.Duration
	usageTTL   time.Duration
}

func (j *janitor) tick(ctx context.Context) {
	j.gcJobs(ctx)
	j.gcOrphanSecrets(ctx)
	j.failStuckBuilds(ctx)
	j.gcUsageEvents(ctx)
}

// gcJobs deletes batch Jobs across tenant-* namespaces whose completion
// timestamp is older than jobTTL. AgentRun CRs typically own these so
// they cascade-delete via OwnerReferences — but only if the CR itself
// is around. We sweep up the rest.
func (j *janitor) gcJobs(ctx context.Context) {
	ns, err := j.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("janitor: list namespaces: %v", err)
		return
	}
	cutoff := time.Now().Add(-j.jobTTL)
	var deleted int
	for _, n := range ns.Items {
		if !strings.HasPrefix(n.Name, "tenant-") {
			continue
		}
		jobs, err := j.kube.BatchV1().Jobs(n.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("janitor: list jobs in %s: %v", n.Name, err)
			continue
		}
		for _, job := range jobs.Items {
			if job.Status.CompletionTime == nil {
				continue
			}
			if job.Status.CompletionTime.Time.After(cutoff) {
				continue
			}
			propagation := metav1.DeletePropagationBackground
			err := j.kube.BatchV1().Jobs(n.Name).Delete(ctx, job.Name, metav1.DeleteOptions{
				PropagationPolicy: &propagation,
			})
			if err != nil {
				log.Printf("janitor: delete job %s/%s: %v", n.Name, job.Name, err)
				continue
			}
			deleted++
		}
	}
	if deleted > 0 {
		log.Printf("janitor: deleted %d completed Jobs older than %s", deleted, j.jobTTL)
	}
}

// gcOrphanSecrets removes "run-<uuid>" Secrets whose corresponding runs
// row no longer exists. Secrets owned by AgentRun CRs are GC'd by k8s
// automatically — this catches the ones where the CR was deleted before
// the dispatch consumer set an OwnerReference, or where the run row was
// pruned by usage retention.
func (j *janitor) gcOrphanSecrets(ctx context.Context) {
	ns, err := j.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	var deleted int
	for _, n := range ns.Items {
		if !strings.HasPrefix(n.Name, "tenant-") {
			continue
		}
		secrets, err := j.kube.CoreV1().Secrets(n.Name).List(ctx, metav1.ListOptions{
			LabelSelector: "ailab.uipath.com/run",
		})
		if err != nil {
			continue
		}
		for _, s := range secrets.Items {
			runID := s.Labels["ailab.uipath.com/run"]
			if runID == "" {
				continue
			}
			var exists bool
			if err := j.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE id::text = $1)`, runID).Scan(&exists); err != nil {
				continue
			}
			if exists {
				continue
			}
			if err := j.kube.CoreV1().Secrets(n.Name).Delete(ctx, s.Name, metav1.DeleteOptions{}); err == nil {
				deleted++
			}
		}
	}
	if deleted > 0 {
		log.Printf("janitor: deleted %d orphan run-Secrets", deleted)
	}
}

// failStuckBuilds marks pending / running builds older than buildStuck
// as failed. These usually come from a builder process that crashed
// mid-Job; the Job will still complete eventually but the row never
// transitions.
func (j *janitor) failStuckBuilds(ctx context.Context) {
	cutoff := time.Now().Add(-j.buildStuck)
	tag, err := j.pool.Exec(ctx, `
		UPDATE builds
		SET status = 'failed', error = COALESCE(error, 'janitor: stuck longer than threshold'),
		    ended_at = now()
		WHERE status IN ('pending','running')
		  AND created_at < $1
	`, cutoff)
	if err != nil {
		log.Printf("janitor: fail stuck builds: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("janitor: failed %d stuck builds (> %s old)", n, j.buildStuck)
	}
}

// gcUsageEvents deletes usage_events older than usageTTL that have
// already been shipped past every destination's checkpoint.
func (j *janitor) gcUsageEvents(ctx context.Context) {
	cutoff := time.Now().Add(-j.usageTTL)
	tag, err := j.pool.Exec(ctx, `
		DELETE FROM usage_events
		WHERE recorded_at < $1
		  AND id <= (SELECT COALESCE(MIN(last_event_id), 0) FROM usage_shipper_state)
	`, cutoff)
	if err != nil {
		log.Printf("janitor: prune usage_events: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("janitor: pruned %d usage_events older than %s", n, j.usageTTL)
	}
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("janitor: %s=%q: %v; using default %s", key, v, err, def)
		return def
	}
	return d
}
