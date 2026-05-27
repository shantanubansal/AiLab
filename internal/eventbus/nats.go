// Package eventbus is the thin wrapper around NATS JetStream that services
// share. It enforces JSON encoding and a single stream + retention policy
// per subject prefix.
package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Handler is invoked for each message a subscriber receives. Returning a
// non-nil error causes the message to be Nak'd and redelivered.
type Handler func(ctx context.Context, data []byte) error

// Bus exposes publish/subscribe for typed JSON payloads.
type Bus struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// Connect dials NATS and prepares JetStream. It also ensures the platform
// stream exists; the stream captures all events under the run.> and build.>
// subject hierarchies with a 7-day retention.
func Connect(ctx context.Context, url string) (*Bus, error) {
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Name("ailab"),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream new: %w", err)
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "ailab",
		Subjects:  []string{"run.>", "build.>", "deployment.>"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Storage:   jetstream.FileStorage,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	return &Bus{nc: nc, js: js}, nil
}

// Publish marshals payload as JSON and publishes to subject. The call returns
// once the broker has acknowledged storage.
func (b *Bus) Publish(ctx context.Context, subject string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := b.js.Publish(ctx, subject, buf); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

// Subscribe creates a durable JetStream consumer over the stream and binds
// it to handler. The durableName is the consumer's stable identity — reuse
// the same name across process restarts to resume where you left off; use
// the same name in multiple processes to fan out via competing consumers.
//
// Handler errors are logged via Nak and the message is redelivered after
// AckWait. Returning nil acknowledges the message.
func (b *Bus) Subscribe(ctx context.Context, durableName, subject string, h Handler) error {
	cons, err := b.js.CreateOrUpdateConsumer(ctx, "ailab", jetstream.ConsumerConfig{
		Durable:       durableName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    10,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("ensure consumer %s: %w", durableName, err)
	}

	_, err = cons.Consume(func(msg jetstream.Msg) {
		hctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h(hctx, msg.Data()); err != nil {
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume %s: %w", durableName, err)
	}
	return nil
}

// Close releases the NATS connection.
func (b *Bus) Close() {
	if b == nil || b.nc == nil {
		return
	}
	b.nc.Close()
}
