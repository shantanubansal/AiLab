// Package billing ships usage_events to a metering destination.
//
// Orb (https://www.withorb.com) is the v1 destination. Stripe meter
// events have the same shape but the ingest URL differs — when we add
// Stripe it'll be a second Destination implementation with the rest of
// the shipper loop unchanged.

package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Event is the destination-agnostic shape we ship.
type Event struct {
	EventName         string         `json:"event_name"`
	ExternalCustomer  string         `json:"external_customer_id"`
	Timestamp         time.Time      `json:"timestamp"`
	IdempotencyKey    string         `json:"idempotency_key"`
	Properties        map[string]any `json:"properties,omitempty"`
}

// Destination is the interface that any billing backend implements.
type Destination interface {
	Name() string
	Ship(ctx context.Context, events []Event) error
}

// OrbClient ships events to Orb's /v1/ingest endpoint.
type OrbClient struct {
	APIKey  string
	BaseURL string // default https://api.withorb.com
	HTTP    *http.Client
}

// NewOrb builds an OrbClient. apiKey is required; baseURL defaults to
// api.withorb.com so it's fine to pass empty for prod.
func NewOrb(apiKey, baseURL string) *OrbClient {
	if baseURL == "" {
		baseURL = "https://api.withorb.com"
	}
	return &OrbClient{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Name identifies this destination in the shipper checkpoint table.
func (c *OrbClient) Name() string { return "orb" }

// Ship posts a batch of events. Orb's docs cap the batch at 500 events
// per call; the shipper loop already enforces that upstream.
func (c *OrbClient) Ship(ctx context.Context, events []Event) error {
	if c.APIKey == "" {
		return fmt.Errorf("orb: API key not configured")
	}
	if len(events) == 0 {
		return nil
	}
	body := map[string]any{"events": events}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/ingest", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("orb %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return nil
}
