// Package telemetry initializes OpenTelemetry tracing across services.
//
// One Init call per service. Reads OTEL_EXPORTER_OTLP_ENDPOINT (gRPC; e.g.
// "tempo.observability:4317") and OTEL_EXPORTER_OTLP_INSECURE; emits to
// that collector. When the endpoint is unset, Init wires a no-op
// TracerProvider so the rest of the platform's tracing calls become
// cheap drops — no error, no special-casing in handlers.
//
// Service identification follows the standard semconv resource attributes
// so traces in Tempo / Grafana group by service name automatically.

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Shutdown is returned by Init and should be called on process exit to
// flush any buffered spans.
type Shutdown func(context.Context) error

// Init configures the global TracerProvider and propagator. Caller passes
// the logical service name ("api", "controller", "builder", etc.). Returns
// a no-op shutdown when no endpoint is configured.
func Init(ctx context.Context, serviceName, version string) (Shutdown, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.TraceContext{})
		return func(context.Context) error { return nil }, nil
	}

	insecure := truthy(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"))
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		errs := []error{
			tp.Shutdown(ctx),
			exp.Shutdown(ctx),
		}
		return errors.Join(errs...)
	}, nil
}

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
