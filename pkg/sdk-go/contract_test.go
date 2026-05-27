// Contract tests for the Go SDK. The shape of every endpoint in
// api/openapi.yaml is canned here as a small handler; each SDK method
// is then exercised against the stub and the typed response struct is
// asserted to contain the expected field values.
//
// Failure mode this catches: someone changes a field name in the api
// DTO (or in the Python/TS SDK), forgets to update the Go SDK, and
// the response decode silently zero-values the field. The Python and
// TypeScript SDKs are tested separately in their own packages; this
// file focuses on Go.

package sdkgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newCannedServer(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()

	// /healthz — unauthenticated.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// /v1/me
	mux.HandleFunc("/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"userId":    "u-1",
			"seatCount": 1,
			"tenant": map[string]any{
				"id":        "t-1",
				"slug":      "acme",
				"name":      "Acme",
				"createdAt": "2026-01-02T03:04:05Z",
			},
		})
	})

	// /v1/agents
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"agents": []map[string]any{{
					"id":        "a-1",
					"name":      "hello",
					"mode":      "job",
					"runtime":   "container",
					"image":     "hello-world",
					"createdAt": "2026-01-02T03:04:05Z",
				}},
			})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{
				"id":        "a-1",
				"name":      "hello",
				"mode":      "job",
				"runtime":   "container",
				"image":     "hello-world",
				"createdAt": "2026-01-02T03:04:05Z",
			})
		}
	})

	// /v1/agents/{agentId} — get/delete
	mux.HandleFunc("/v1/agents/a-1", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"id": "a-1", "name": "hello", "mode": "job",
				"runtime": "container", "image": "hello-world",
				"createdAt": "2026-01-02T03:04:05Z",
			})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	// /v1/agents/{agentId}/runs — list + trigger
	mux.HandleFunc("/v1/agents/a-1/runs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"runs": []map[string]any{{
					"id":        "r-1",
					"agentId":   "a-1",
					"status":    "succeeded",
					"createdAt": "2026-01-02T03:04:05Z",
				}},
			})
		case http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, map[string]any{
				"id":        "r-1",
				"agentId":   "a-1",
				"status":    "pending",
				"traceId":   "trace-abc",
				"createdAt": "2026-01-02T03:04:05Z",
				"inputs":    map[string]any{"foo": "bar"},
			})
		}
	})

	// /v1/runs/{runId}
	mux.HandleFunc("/v1/runs/r-1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"id":        "r-1",
			"agentId":   "a-1",
			"status":    "succeeded",
			"exitCode":  0,
			"createdAt": "2026-01-02T03:04:05Z",
			"startedAt": "2026-01-02T03:04:06Z",
			"endedAt":   "2026-01-02T03:04:10Z",
		})
	})

	// /v1/runs/{runId}/logs — SSE
	mux.HandleFunc("/v1/runs/r-1/logs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: line one\n\ndata: line two\n\n"))
	})

	// /v1/agents/{agentId}/triggers — list + create
	mux.HandleFunc("/v1/agents/a-1/triggers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"triggers": []map[string]any{{
					"id":        "tr-1",
					"agentId":   "a-1",
					"kind":      "webhook",
					"name":      "incoming",
					"createdAt": "2026-01-02T03:04:05Z",
				}},
			})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{
				"id":            "tr-1",
				"agentId":       "a-1",
				"kind":          "webhook",
				"name":          "incoming",
				"createdAt":     "2026-01-02T03:04:05Z",
				"webhookSecret": "shh",
			})
		}
	})

	// /v1/agents/{agentId}/deploy
	mux.HandleFunc("/v1/agents/a-1/deploy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	// /v1/agents/{agentId}/builds
	mux.HandleFunc("/v1/agents/a-1/builds", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]any{
			"id":        "b-1",
			"agentId":   "a-1",
			"status":    "pending",
			"createdAt": "2026-01-02T03:04:05Z",
		})
	})

	// /v1/secrets
	mux.HandleFunc("/v1/secrets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"secrets": []map[string]any{{
					"name":      "MY_API_KEY",
					"createdAt": "2026-01-02T03:04:05Z",
					"updatedAt": "2026-01-02T03:04:05Z",
				}},
			})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{
				"name":      "MY_API_KEY",
				"createdAt": "2026-01-02T03:04:05Z",
				"updatedAt": "2026-01-02T03:04:05Z",
			})
		}
	})

	// /v1/secrets/{name}
	mux.HandleFunc("/v1/secrets/MY_API_KEY", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	return New(srv.URL, "test-token"), srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestSDKMethods(t *testing.T) {
	cl, srv := newCannedServer(t)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("Me", func(t *testing.T) {
		m, err := cl.Me(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.UserID != "u-1" || m.SeatCount != 1 || m.Tenant.ID != "t-1" || m.Tenant.Slug != "acme" {
			t.Fatalf("unexpected: %+v", m)
		}
	})

	t.Run("ListAgents", func(t *testing.T) {
		out, err := cl.ListAgents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].ID != "a-1" || out[0].Image == nil || *out[0].Image != "hello-world" {
			t.Fatalf("unexpected: %+v", out)
		}
	})

	t.Run("CreateAgent", func(t *testing.T) {
		a, err := cl.CreateAgent(ctx, map[string]any{"name": "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if a.ID != "a-1" || a.Mode != "job" {
			t.Fatalf("unexpected: %+v", a)
		}
	})

	t.Run("GetAgent", func(t *testing.T) {
		a, err := cl.GetAgent(ctx, "a-1")
		if err != nil {
			t.Fatal(err)
		}
		if a.ID != "a-1" || a.Runtime != "container" {
			t.Fatalf("unexpected: %+v", a)
		}
	})

	t.Run("DeleteAgent", func(t *testing.T) {
		if err := cl.DeleteAgent(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListRuns", func(t *testing.T) {
		out, err := cl.ListRuns(ctx, "a-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Status != "succeeded" {
			t.Fatalf("unexpected: %+v", out)
		}
	})

	t.Run("TriggerRun", func(t *testing.T) {
		r, err := cl.TriggerRun(ctx, "a-1", map[string]any{"foo": "bar"})
		if err != nil {
			t.Fatal(err)
		}
		if r.ID != "r-1" || r.Status != "pending" || r.TraceID != "trace-abc" {
			t.Fatalf("unexpected: %+v", r)
		}
	})

	t.Run("GetRun", func(t *testing.T) {
		r, err := cl.GetRun(ctx, "r-1")
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "succeeded" || r.ExitCode == nil || *r.ExitCode != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
		if r.StartedAt == nil || r.EndedAt == nil {
			t.Fatal("startedAt / endedAt missing")
		}
	})

	t.Run("StreamLogs", func(t *testing.T) {
		var lines []string
		err := cl.StreamLogs(ctx, "r-1", func(s string) { lines = append(lines, s) })
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
			t.Fatalf("unexpected: %v", lines)
		}
	})

	t.Run("ListTriggers", func(t *testing.T) {
		out, err := cl.ListTriggers(ctx, "a-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Kind != "webhook" {
			t.Fatalf("unexpected: %+v", out)
		}
	})

	t.Run("CreateWebhookTrigger", func(t *testing.T) {
		out, err := cl.CreateWebhookTrigger(ctx, "a-1", "incoming")
		if err != nil {
			t.Fatal(err)
		}
		if out.WebhookSecret != "shh" {
			t.Fatalf("expected webhookSecret to surface once; got %q", out.WebhookSecret)
		}
	})

	t.Run("CreateCronTrigger", func(t *testing.T) {
		out, err := cl.CreateCronTrigger(ctx, "a-1", "every-5m", "*/5 * * * *")
		if err != nil {
			t.Fatal(err)
		}
		if out.ID != "tr-1" {
			t.Fatalf("unexpected: %+v", out)
		}
	})

	t.Run("Deploy/Undeploy", func(t *testing.T) {
		if err := cl.Deploy(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
		if err := cl.Undeploy(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CreateBuild", func(t *testing.T) {
		b, err := cl.CreateBuild(ctx, "a-1", "git+https://example.com/x.git")
		if err != nil {
			t.Fatal(err)
		}
		if b.ID != "b-1" || b.Status != "pending" {
			t.Fatalf("unexpected: %+v", b)
		}
	})

	t.Run("Secrets", func(t *testing.T) {
		out, err := cl.ListSecrets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Name != "MY_API_KEY" {
			t.Fatalf("unexpected: %+v", out)
		}
		ref, err := cl.SetSecret(ctx, "MY_API_KEY", "value")
		if err != nil {
			t.Fatal(err)
		}
		if ref.Name != "MY_API_KEY" {
			t.Fatalf("unexpected: %+v", ref)
		}
		if err := cl.DeleteSecret(ctx, "MY_API_KEY"); err != nil {
			t.Fatal(err)
		}
	})
}

// TestSDKBearerHeader confirms the SDK actually sets Authorization on
// outgoing requests. The other tests rely on the stub being forgiving;
// this one inspects the real header explicitly.
func TestSDKBearerHeader(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		writeJSON(w, map[string]any{"agents": []any{}})
	}))
	t.Cleanup(srv.Close)

	cl := New(srv.URL, "my-bearer")
	if _, err := cl.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen, "Bearer ") || seen != "Bearer my-bearer" {
		t.Fatalf("expected Authorization=Bearer my-bearer; got %q", seen)
	}
}
