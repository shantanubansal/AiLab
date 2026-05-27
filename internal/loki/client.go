// Package loki is a thin client over the Loki HTTP API. The api uses it
// to back GET /v1/runs/{id}/logs after the pod has terminated; while the
// pod is live, k8s pod-log streaming is faster + simpler so we stay on
// that path.
//
// The client only implements what we need: query_range, sorted forward,
// returning log lines as plain strings. Expand on demand.
package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a Loki query client.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client. baseURL is e.g. "http://loki.observability:3100".
// Empty baseURL produces a non-functional client whose Query() returns
// (nil, ErrDisabled) — that lets callers gate logically.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Disabled reports whether the client has no configured base URL.
func (c *Client) Disabled() bool { return c == nil || c.BaseURL == "" }

// ErrDisabled is returned by Query when no Loki URL was configured.
type errDisabled struct{}

func (errDisabled) Error() string { return "loki: disabled (LOKI_URL unset)" }

// ErrDisabled marker singleton.
var ErrDisabled error = errDisabled{}

// LogQLForRun returns a query that selects all log lines for a given run.
// We label on the pod name because k8s-label-to-loki-label mapping varies
// across log shippers (Promtail vs Vector vs Grafana Agent); the pod name
// is universal.
func LogQLForRun(tenantID, runID string) string {
	return fmt.Sprintf(`{namespace="tenant-%s", pod=~"run-%s-.*"}`, tenantID, runID)
}

// Query runs a query_range against Loki and returns the matched lines in
// ascending timestamp order. start/end are wall-clock; pass zero values
// to default to "last 24h ... now".
func (c *Client) Query(ctx context.Context, query string, start, end time.Time, limit int) ([]string, error) {
	if c.Disabled() {
		return nil, ErrDisabled
	}
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end.Add(-24 * time.Hour)
	}
	if limit <= 0 {
		limit = 5000
	}
	u, err := url.Parse(c.BaseURL + "/loki/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("direction", "forward")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("loki %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"` // [ns, line]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode loki response: %w", err)
	}
	// Merge all series into one slice, preserving forward order.
	var lines []string
	for _, s := range out.Data.Result {
		for _, v := range s.Values {
			lines = append(lines, v[1])
		}
	}
	return lines, nil
}
