// Daily S3 export of the audit log. Reads new audit_events past the
// checkpoint, gzips them as JSON Lines, uploads to
// s3://<bucket>/<prefix>/dt=YYYY-MM-DD/<batch-id>.jsonl.gz, advances
// the checkpoint only on successful upload.
//
// Skip-silently semantics: when bucket is empty, Run() is a no-op so
// the janitor can call it unconditionally on every tick.

package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/shantanubansal/AiLab/internal/db"
)

// ExporterConfig holds the destination bucket + key prefix.
type ExporterConfig struct {
	Bucket    string
	Prefix    string // e.g. "audit"; defaults to "audit"
	Region    string // optional; SDK falls back to AWS_REGION or instance metadata
	BatchSize int    // max events per upload; defaults to 5000
}

// Exporter writes audit_events to S3 in batches.
type Exporter struct {
	pool   *db.Pool
	s3     *s3.Client
	bucket string
	prefix string
	batch  int
}

// NewExporter resolves AWS config and returns an Exporter, or (nil, nil)
// when cfg.Bucket is empty so callers can use the result unconditionally.
func NewExporter(ctx context.Context, pool *db.Pool, cfg ExporterConfig) (*Exporter, error) {
	if cfg.Bucket == "" {
		return nil, nil
	}
	opts := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "audit"
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 5000
	}
	return &Exporter{
		pool:   pool,
		s3:     s3.NewFromConfig(awsCfg),
		bucket: cfg.Bucket,
		prefix: strings.Trim(prefix, "/"),
		batch:  batch,
	}, nil
}

// Run executes one export pass. Returns nil when there are no new events.
func (e *Exporter) Run(ctx context.Context) error {
	if e == nil {
		return nil
	}
	const destination = "s3"
	var lastID int64
	if err := e.pool.QueryRow(ctx, `
		SELECT last_event_id FROM audit_export_state WHERE destination = $1
	`, destination).Scan(&lastID); err != nil {
		return fmt.Errorf("checkpoint read: %w", err)
	}

	rows, err := e.pool.Query(ctx, `
		SELECT id, tenant_id, COALESCE(user_id, ''), action, resource_type,
		       COALESCE(resource_id, ''), metadata, COALESCE(request_id, ''),
		       created_at
		FROM audit_events
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, lastID, e.batch)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var (
		buf       bytes.Buffer
		gz        = gzip.NewWriter(&buf)
		enc       = json.NewEncoder(gz)
		count     int
		newestID  int64
	)
	for rows.Next() {
		var (
			id           int64
			tenantID     string
			userID       string
			action       string
			resourceType string
			resourceID   string
			metadata     []byte
			requestID    string
			createdAt    time.Time
		)
		if err := rows.Scan(&id, &tenantID, &userID, &action, &resourceType, &resourceID, &metadata, &requestID, &createdAt); err != nil {
			return err
		}
		// Emit one line per event. metadata stays as a raw JSON message
		// so we don't pay the cost of re-parsing on the way out.
		out := struct {
			ID           int64           `json:"id"`
			TenantID     string          `json:"tenantId"`
			UserID       string          `json:"userId,omitempty"`
			Action       string          `json:"action"`
			ResourceType string          `json:"resourceType"`
			ResourceID   string          `json:"resourceId,omitempty"`
			Metadata     json.RawMessage `json:"metadata,omitempty"`
			RequestID    string          `json:"requestId,omitempty"`
			CreatedAt    time.Time       `json:"createdAt"`
		}{id, tenantID, userID, action, resourceType, resourceID, json.RawMessage(metadata), requestID, createdAt}
		if err := enc.Encode(&out); err != nil {
			return err
		}
		count++
		newestID = id
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if err := gz.Close(); err != nil {
		return err
	}

	now := time.Now().UTC()
	key := fmt.Sprintf("%s/dt=%s/%s.jsonl.gz", e.prefix, now.Format("2006-01-02"), uuid.NewString())
	if _, err := e.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(e.bucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("application/x-ndjson"),
		ContentEncoding: aws.String("gzip"),
	}); err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}

	if _, err := e.pool.Exec(ctx, `
		UPDATE audit_export_state
		SET last_event_id = $2, last_exported_at = now()
		WHERE destination = $1 AND last_event_id < $2
	`, destination, newestID); err != nil {
		return fmt.Errorf("advance checkpoint: %w", err)
	}
	return nil
}
