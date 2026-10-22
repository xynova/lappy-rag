package tracing

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xynova/lappy-rag/internal/observability"
)

func TestTracingWiringHappyPath(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()

	ctx, rootSpan := observability.WithOpenTelemetryTrace(context.Background(), "test-service", "wiring.root")
	var rootErr error

	ctx = WithChainContext(ctx, ChainType("translation"), "test-service")
	interceptor := NewOpenInferenceInterceptor(true, "test-service")

	wrapped := interceptor.Wrap(ModuleInfo{Name: "generator.module", Type: "generator", ModelName: "gemma", Provider: "localai"}, func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"answer": "ok"}, nil
	})

	output, err := wrapped(ctx, map[string]interface{}{"question": "ping"})
	if err != nil {
		t.Fatalf("wrapped handler failed: %v", err)
	}
	if output["answer"] != "ok" {
		t.Fatalf("expected answer=ok, got %v", output["answer"])
	}

	EndChainSpanIfPresent(ctx, &rootErr)
	observability.EndSpanWithStatus(rootSpan, &rootErr)

	spans := recorder.Ended()
	if len(spans) < 3 {
		t.Fatalf("expected at least 3 spans, got %d", len(spans))
	}

	root := findByName(spans, "wiring.root")
	chain := findByName(spans, "translation.chain")
	module := findByName(spans, "generator.module")
	if root == nil || chain == nil || module == nil {
		t.Fatalf("expected root, chain, and module spans to exist")
	}

	if root.SpanContext().TraceID() != chain.SpanContext().TraceID() || root.SpanContext().TraceID() != module.SpanContext().TraceID() {
		t.Fatalf("expected all spans to share one trace id")
	}
	if chain.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatalf("expected chain parent to be root")
	}
	if module.Parent().SpanID() != chain.SpanContext().SpanID() {
		t.Fatalf("expected module parent to be chain")
	}

	assertAttr(t, module.Attributes(), attribute.String(OpenInferenceSpanKindKey, OpenInferenceSpanKindLLM))
}

func TestTracingWiringErrorPropagation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()

	ctx, rootSpan := observability.WithOpenTelemetryTrace(context.Background(), "test-service", "wiring.error")
	ctx = WithChainContext(ctx, ChainType("topic"), "test-service")
	interceptor := NewOpenInferenceInterceptor(true, "test-service")

	rootErr := errors.New("module failed")
	wrapped := interceptor.Wrap(ModuleInfo{Name: "evaluator.module"}, func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return nil, rootErr
	})

	_, err := wrapped(ctx, map[string]interface{}{"question": "ping"})
	if err == nil {
		t.Fatal("expected wrapped handler error")
	}

	EndChainSpanIfPresent(ctx, &err)
	observability.EndSpanWithStatus(rootSpan, &err)

	spans := recorder.Ended()
	root := findByName(spans, "wiring.error")
	chain := findByName(spans, "topic.chain")
	module := findByName(spans, "evaluator.module")
	if root == nil || chain == nil || module == nil {
		t.Fatalf("expected root, chain, and module spans to exist")
	}
	if root.Status().Code != codes.Error || chain.Status().Code != codes.Error || module.Status().Code != codes.Error {
		t.Fatalf("expected root, chain, and module spans to be error status; got root=%v chain=%v module=%v", root.Status().Code, chain.Status().Code, module.Status().Code)
	}
}

func TestTracingWiringInterceptorDisabled(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()

	ctx, rootSpan := observability.WithOpenTelemetryTrace(context.Background(), "test-service", "wiring.disabled")
	rootErr := error(nil)
	interceptor := NewOpenInferenceInterceptor(false, "test-service")

	wrapped := interceptor.Wrap(ModuleInfo{Name: "disabled.module"}, func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"ok": true}, nil
	})

	_, err := wrapped(ctx, map[string]interface{}{"question": "ping"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	observability.EndSpanWithStatus(rootSpan, &rootErr)
	spans := recorder.Ended()

	if findByName(spans, "disabled.module") != nil {
		t.Fatalf("expected no module span when interceptor is disabled")
	}
}

func findByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	return nil
}

func assertAttr(t *testing.T, attrs []attribute.KeyValue, expected attribute.KeyValue) {
	t.Helper()
	for _, attr := range attrs {
		if attr.Key == expected.Key && attr.Value.AsString() == expected.Value.AsString() {
			return
		}
	}
	t.Fatalf("expected attribute %s=%s to be present", expected.Key, expected.Value.AsString())
}
