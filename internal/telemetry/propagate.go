// Carriers + helpers for propagating trace context through NATS event
// payloads. The map representation is portable (no NATS-specific
// headers) and round-trips through JSON cleanly.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// mapCarrier adapts map[string]string to the TextMapCarrier interface so
// otel propagators can inject/extract on a JSON-friendly value.
type mapCarrier map[string]string

func (m mapCarrier) Get(key string) string  { return m[key] }
func (m mapCarrier) Set(key, value string)  { m[key] = value }
func (m mapCarrier) Keys() []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Inject pulls the active span context out of ctx and serializes it
// into a portable map (W3C traceparent / tracestate). Returns nil when
// no active context exists so callers can store nil into omitempty fields.
func Inject(ctx context.Context) map[string]string {
	carrier := mapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return carrier
}

// Extract reads a previously Inject()'d map back into the context so a
// subsequent tracer.Start() resumes as a child of the original span.
func Extract(ctx context.Context, carrier map[string]string) context.Context {
	if len(carrier) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, mapCarrier(carrier))
}

// Propagator exposes the global propagator for tests + callers that
// want to bypass the helpers (rare).
func Propagator() propagation.TextMapPropagator { return otel.GetTextMapPropagator() }
