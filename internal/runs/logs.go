// Pod log streaming. GET /v1/runs/{runId}/logs is an SSE stream with two
// possible backends:
//   * live: a Pod for this run still exists → kubectl-style follow
//   * historical: no Pod left → Loki query_range (if configured)
//
// The handler picks the right backend transparently. Pods get garbage-
// collected after their Job finishes, so the Loki path is what makes
// "show me logs for a run that finished yesterday" work.

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
	"github.com/shantanubansal/AiLab/internal/loki"
)

// streamLogs is the handler for GET /v1/runs/{runId}/logs.
func (h *Handlers) streamLogs(w http.ResponseWriter, r *http.Request) {
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	setSSEHeaders(w)

	// 1. Live path. If a Pod still exists for this run, stream from kubectl.
	if h.K8s != nil {
		ns := "tenant-" + run.TenantID
		pod, podErr := findPod(r.Context(), h.K8s, ns, runID)
		if podErr == nil {
			h.streamFromPod(r.Context(), w, flusher, ns, pod)
			return
		}
	}

	// 2. Historical path. Loki, if configured.
	if h.Loki != nil && !h.Loki.Disabled() {
		h.streamFromLoki(r.Context(), w, flusher, run.TenantID, runID)
		return
	}

	fmt.Fprint(w, "event: error\ndata: no live pod and Loki not configured\n\n")
	flusher.Flush()
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) streamFromPod(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, namespace, pod string) {
	stream, err := h.K8s.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Follow:    true,
		Container: "agent",
	}).Stream(ctx)
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

func (h *Handlers) streamFromLoki(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, tenantID, runID string) {
	lines, err := h.Loki.Query(ctx, loki.LogQLForRun(tenantID, runID), time.Time{}, time.Time{}, 0)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	for _, line := range lines {
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	}
}

// findPod looks up the (single) Pod tagged with this run's id. Quick
// non-blocking version of waitForPod — we treat "no pod" as a signal
// to fall back to Loki rather than as a wait condition.
func findPod(ctx context.Context, k kubernetes.Interface, namespace, runID string) (string, error) {
	pods, err := k.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "ailab.uipath.com/run=" + runID,
		Limit:         1,
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", errors.New("no pod for run")
	}
	return pods.Items[0].Name, nil
}
