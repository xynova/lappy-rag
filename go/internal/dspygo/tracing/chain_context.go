package tracing

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xynova/lappy-rag/internal/observability"
)

// ChainType identifies a pipeline/job chain kind.
type ChainType string

type chainSpanContextKey struct{}

type chainScope struct {
	ctx  context.Context
	span trace.Span
}

// ChainSpanCtxKeyType returns the key used to stash chain span context.
func ChainSpanCtxKeyType() interface{} {
	return chainSpanContextKey{}
}

// WithChainContext creates a chain span and stores it for downstream workflow/LLM spans.
func WithChainContext(ctx context.Context, chainType ChainType, serviceName string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	resolvedChainType := strings.TrimSpace(string(chainType))
	if resolvedChainType == "" {
		resolvedChainType = "default"
	}

	resolvedServiceName := strings.TrimSpace(serviceName)
	if resolvedServiceName == "" {
		resolvedServiceName = observability.DefaultServiceName
	}

	tracer := otel.Tracer(resolvedServiceName)
	chainCtx, chainSpan := tracer.Start(
		ctx,
		resolvedChainType+".chain",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(OpenInferenceSpanKindKey, OpenInferenceSpanKindChain),
			attribute.String("chain.type", resolvedChainType),
			attribute.String("service.name", resolvedServiceName),
		),
	)

	scope := chainScope{ctx: chainCtx, span: chainSpan}
	return context.WithValue(chainCtx, chainSpanContextKey{}, scope)
}

// ChainContext returns the chain context when present.
func ChainContext(ctx context.Context) (context.Context, bool) {
	if ctx == nil {
		return nil, false
	}

	scope, ok := ctx.Value(chainSpanContextKey{}).(chainScope)
	if !ok {
		return nil, false
	}

	return scope.ctx, true
}

// EndChainSpanIfPresent closes the chain span when present in the context.
func EndChainSpanIfPresent(ctx context.Context, err *error) {
	if ctx == nil {
		return
	}

	scope, ok := ctx.Value(chainSpanContextKey{}).(chainScope)
	if !ok {
		return
	}

	observability.EndSpanWithStatus(scope.span, err)
}
