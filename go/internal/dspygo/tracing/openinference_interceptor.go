package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/xynova/lappy-rag/internal/observability"
)

// Handler is a generic DSPy module execution function.
type Handler func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

// ModuleInfo describes a traced module call.
type ModuleInfo struct {
	Name      string
	Type      string
	ModelName string
	Provider  string
}

// OpenInferenceInterceptor instruments module execution with LLM spans.
type OpenInferenceInterceptor struct {
	enabled     bool
	serviceName string
}

// NewOpenInferenceInterceptor creates an interceptor instance.
func NewOpenInferenceInterceptor(enabled bool, serviceName string) *OpenInferenceInterceptor {
	resolvedServiceName := strings.TrimSpace(serviceName)
	if resolvedServiceName == "" {
		resolvedServiceName = observability.DefaultServiceName
	}

	return &OpenInferenceInterceptor{enabled: enabled, serviceName: resolvedServiceName}
}

// Wrap decorates a module handler with an OpenInference LLM span.
func (i *OpenInferenceInterceptor) Wrap(module ModuleInfo, next Handler) Handler {
	if next == nil {
		panic("next handler cannot be nil")
	}

	if i == nil || !i.enabled {
		return next
	}

	return func(ctx context.Context, input map[string]interface{}) (output map[string]interface{}, err error) {
		parentCtx := ctx
		if chainCtx, ok := ChainContext(ctx); ok {
			parentCtx = chainCtx
		}
		if parentCtx == nil {
			parentCtx = context.Background()
		}

		moduleName := strings.TrimSpace(module.Name)
		if moduleName == "" {
			moduleName = "llm.module"
		}

		tracer := otel.Tracer(i.serviceName)
		spanCtx, span := tracer.Start(
			parentCtx,
			moduleName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String(OpenInferenceSpanKindKey, OpenInferenceSpanKindLLM),
				attribute.String("module.name", moduleName),
				attribute.String("module.type", strings.TrimSpace(module.Type)),
				attribute.String("llm.model_name", strings.TrimSpace(module.ModelName)),
				attribute.String("llm.provider", strings.TrimSpace(module.Provider)),
			),
		)

		if len(input) > 0 {
			span.SetAttributes(attribute.String("input.value", mustJSON(input)))
		}

		defer func() {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				return
			}

			if len(output) > 0 {
				span.SetAttributes(attribute.String("output.value", mustJSON(output)))
			}
			span.SetStatus(codes.Ok, "ok")
			span.End()
		}()

		output, err = next(spanCtx, input)
		if err != nil {
			return nil, fmt.Errorf("run module %q: %w", moduleName, err)
		}

		return output, nil
	}
}

func mustJSON(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
