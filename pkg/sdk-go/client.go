// Package sdkgo is the Go client for the AiLab platform API.
//
// It mirrors the OpenAPI in /api and powers the agentctl CLI. External
// Go consumers (CI/CD glue, internal tools) import this package directly.
package sdkgo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the platform client. Construct with New.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a Client. baseURL is e.g. "http://localhost:8080".
// token is the bearer token; for dev: "dev:<tenantId>:<userId>".
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ---- Types matching api DTOs ----

type Agent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	Runtime   string    `json:"runtime"`
	Image     *string   `json:"image"`
	CreatedAt time.Time `json:"createdAt"`
}

type Run struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agentId"`
	Status    string          `json:"status"`
	Inputs    json.RawMessage `json:"inputs,omitempty"`
	Outputs   json.RawMessage `json:"outputs,omitempty"`
	ExitCode  *int            `json:"exitCode,omitempty"`
	TraceID   string          `json:"traceId,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	StartedAt *time.Time      `json:"startedAt,omitempty"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
}

type Trigger struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agentId"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	Cron          string    `json:"cron,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	WebhookSecret string    `json:"webhookSecret,omitempty"`
}

type Build struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agentId"`
	Status    string     `json:"status"`
	Image     string     `json:"image,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

type SecretRef struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// APIError is returned when the server responds non-2xx.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("api %d: %s", e.Status, e.Body) }

// ---- Core request plumbing ----

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, buf)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// ---- Agents ----

func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var resp struct {
		Agents []Agent `json:"agents"`
	}
	err := c.do(ctx, "GET", "/v1/agents", nil, &resp)
	return resp.Agents, err
}

func (c *Client) CreateAgent(ctx context.Context, manifest map[string]any) (Agent, error) {
	var a Agent
	err := c.do(ctx, "POST", "/v1/agents", map[string]any{"manifest": manifest}, &a)
	return a, err
}

func (c *Client) GetAgent(ctx context.Context, id string) (Agent, error) {
	var a Agent
	err := c.do(ctx, "GET", "/v1/agents/"+id, nil, &a)
	return a, err
}

func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/agents/"+id, nil, nil)
}

// ---- Runs ----

func (c *Client) ListRuns(ctx context.Context, agentID string) ([]Run, error) {
	var resp struct {
		Runs []Run `json:"runs"`
	}
	err := c.do(ctx, "GET", "/v1/agents/"+agentID+"/runs", nil, &resp)
	return resp.Runs, err
}

func (c *Client) TriggerRun(ctx context.Context, agentID string, inputs map[string]any) (Run, error) {
	var r Run
	err := c.do(ctx, "POST", "/v1/agents/"+agentID+"/runs", map[string]any{"inputs": inputs}, &r)
	return r, err
}

func (c *Client) GetRun(ctx context.Context, runID string) (Run, error) {
	var r Run
	err := c.do(ctx, "GET", "/v1/runs/"+runID, nil, &r)
	return r, err
}

// StreamLogs streams the SSE log frames for a run, calling onLine for each
// emitted line. Returns when the stream ends or ctx is cancelled.
func (c *Client) StreamLogs(ctx context.Context, runID string, onLine func(string)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/v1/runs/"+runID+"/logs", nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			onLine(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// ---- Triggers ----

func (c *Client) ListTriggers(ctx context.Context, agentID string) ([]Trigger, error) {
	var resp struct {
		Triggers []Trigger `json:"triggers"`
	}
	err := c.do(ctx, "GET", "/v1/agents/"+agentID+"/triggers", nil, &resp)
	return resp.Triggers, err
}

// CreateWebhookTrigger returns the trigger including the plaintext webhook
// secret — surface this to the user; the platform never reveals it again.
func (c *Client) CreateWebhookTrigger(ctx context.Context, agentID, name string) (Trigger, error) {
	var t Trigger
	err := c.do(ctx, "POST", "/v1/agents/"+agentID+"/triggers", map[string]any{
		"kind": "webhook",
		"name": name,
	}, &t)
	return t, err
}

func (c *Client) CreateCronTrigger(ctx context.Context, agentID, name, cronExpr string) (Trigger, error) {
	var t Trigger
	err := c.do(ctx, "POST", "/v1/agents/"+agentID+"/triggers", map[string]any{
		"kind": "cron",
		"name": name,
		"cron": cronExpr,
	}, &t)
	return t, err
}

// ---- Deploy ----

func (c *Client) Deploy(ctx context.Context, agentID string) error {
	return c.do(ctx, "POST", "/v1/agents/"+agentID+"/deploy", nil, nil)
}

func (c *Client) Undeploy(ctx context.Context, agentID string) error {
	return c.do(ctx, "DELETE", "/v1/agents/"+agentID+"/deploy", nil, nil)
}

// ---- Builds ----

func (c *Client) CreateBuild(ctx context.Context, agentID, sourceURL string) (Build, error) {
	var b Build
	err := c.do(ctx, "POST", "/v1/agents/"+agentID+"/builds", map[string]any{
		"sourceUrl": sourceURL,
	}, &b)
	return b, err
}

// ---- Secrets ----

func (c *Client) ListSecrets(ctx context.Context) ([]SecretRef, error) {
	var resp struct {
		Secrets []SecretRef `json:"secrets"`
	}
	err := c.do(ctx, "GET", "/v1/secrets", nil, &resp)
	return resp.Secrets, err
}

// SetSecret creates or updates a tenant secret by name.
func (c *Client) SetSecret(ctx context.Context, name, value string) (SecretRef, error) {
	var s SecretRef
	err := c.do(ctx, "POST", "/v1/secrets", map[string]any{
		"name":  name,
		"value": value,
	}, &s)
	return s, err
}

func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/secrets/"+name, nil, nil)
}
