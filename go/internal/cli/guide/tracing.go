package guide

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/xynova/lappy-rag/internal/observability"
)

// StartCommandTrace starts a root span for a CLI command execution.
func StartCommandTrace(ctx context.Context, cfg observability.OpenInferenceConfig, operationName string) (context.Context, trace.Span) {
	return observability.WithOpenTelemetryTrace(ctx, observability.ServiceNameFromConfig(&cfg), operationName)
}
