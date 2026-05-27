// SSE endpoint for live run lifecycle updates. The UI uses this in
// place of polling /v1/runs/{id} every couple of seconds.
//
// Each accepted connection registers a per-runId subscription on the
// Hub, then forwards every event as a JSON SSE frame:
//
//   event: started
//   data:  {"tenantId":"...","agentId":"...","runId":"...","at":"..."}
//
//   event: completed
//   data:  {"tenantId":"...","runId":"...","status":"succeeded","exitCode":0,...}
//
// The handler closes after run.completed (terminal state) or when the
// client disconnects.

package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/db"
)

func (h *Handlers) streamEvents(w http.ResponseWriter, r *http.Request) {
	if h.Hub == nil {
		http.Error(w, "events unavailable: hub not configured", http.StatusServiceUnavailable)
		return
	}
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	runID := chi.URLParam(r, "runId")

	// Verify the run exists for this tenant. Without this check, any
	// authenticated user could probe for run ids by listening for events
	// on someone else's run id and seeing whether any arrive.
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

	ch, cancel := h.Hub.Subscribe(runID)
	defer cancel()

	setSSEHeaders(w)

	// If the run is already terminal, emit one synthetic completed
	// event so a late subscriber doesn't hang forever.
	if isTerminal(run.Status) {
		writeEvent(w, flusher, "completed", terminalSummary(run))
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Kind {
			case "started":
				if ev.Started != nil {
					writeEvent(w, flusher, "started", ev.Started)
				}
			case "completed":
				if ev.Completed != nil {
					writeEvent(w, flusher, "completed", ev.Completed)
				}
				return
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, name string, payload any) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, buf)
	flusher.Flush()
}

func isTerminal(s Status) bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled:
		return true
	}
	return false
}

// terminalSummary builds a minimal payload for a terminal run that
// arrived after we missed the original NATS event (cold subscribe).
func terminalSummary(run Run) map[string]any {
	out := map[string]any{
		"tenantId": run.TenantID,
		"runId":    run.ID,
		"status":   string(run.Status),
	}
	if run.ExitCode != nil {
		out["exitCode"] = *run.ExitCode
	}
	if run.EndedAt != nil {
		out["endedAt"] = *run.EndedAt
	}
	if run.StartedAt != nil {
		out["startedAt"] = *run.StartedAt
	}
	return out
}
