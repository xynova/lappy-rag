package observability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// InitOTEL configures and registers a global OTEL tracer provider.
func InitOTEL(serviceName, endpoint string) (*sdktrace.TracerProvider, error) {
	trimmedEndpoint := sanitizeOTLPEndpoint(endpoint)
	if trimmedEndpoint == "" {
		return nil, fmt.Errorf("openinference endpoint is required")
	}

	exporter, err := otlptracegrpc.New(
		context.Background(),
		otlptracegrpc.WithEndpoint(trimmedEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize otlp grpc exporter: %w", err)
	}

	resource := sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(strings.TrimSpace(serviceName)),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(3*time.Second)),
		sdktrace.WithResource(resource),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}

// ShutdownOTEL flushes and closes the OTEL tracer provider.
func ShutdownOTEL(ctx context.Context, provider *sdktrace.TracerProvider) error {
	if provider == nil {
		return nil
	}

	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown otel tracer provider: %w", err)
	}

	return nil
}

// ForceFlushOTEL flushes pending spans from the provider.
func ForceFlushOTEL(ctx context.Context, provider *sdktrace.TracerProvider) error {
	if provider == nil {
		return nil
	}

	if err := provider.ForceFlush(ctx); err != nil {
		return fmt.Errorf("force flush otel tracer provider: %w", err)
	}

	return nil
}

func sanitizeOTLPEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	return strings.TrimRight(trimmed, "/")
}
