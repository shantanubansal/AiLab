// Pod log streaming. Translates GET /v1/runs/{runId}/logs into an SSE
// stream of the matching pod's stdout/stderr. Stays simple in v1: one
// matching pod, follow=true until the pod ends, then close.
//
// In v1.1 this becomes a Loki query so historical logs are available
// after the pod has been garbage-collected; until then the agent's stderr
// is reachable only while the pod is around.

package runs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

// streamLogs is wired by Handlers when a kubernetes client is configured.
func (h *Handlers) streamLogs(w http.ResponseWriter, r *http.Request) {
	if h.K8s == nil {
		http.Error(w, "logs unavailable: kube client not configured", http.StatusServiceUnavailable)
		return
	}

	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	runID := chi.URLParam(r, "runId")

	run, err := h.Runs.Get(r.Context(), id.TenantID, runID)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ns := "tenant-" + run.TenantID
	pod, err := waitForPod(r.Context(), h.K8s, ns, runID, 15*time.Second)
	if err != nil {
		http.Error(w, "log target not found: "+err.Error(), http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	stream, err := h.K8s.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{
		Follow:     true,
		Timestamps: false,
		Container:  "agent",
	}).Stream(r.Context())
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
	}
}

// waitForPod polls for a Pod labeled with the run id, up to deadline. The
// reconciler's Job + Pod creation is racey vs an eager UI subscribe, so we
// give it a short grace window before declaring not-found.
func waitForPod(ctx context.Context, k kubernetes.Interface, namespace, runID string, within time.Duration) (string, error) {
	deadline := time.Now().Add(within)
	for {
		pods, err := k.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "ailab.uipath.com/run=" + runID,
			Limit:         1,
		})
		if err == nil && len(pods.Items) > 0 {
			return pods.Items[0].Name, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", err
			}
			return "", errors.New("no pod found for run")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
