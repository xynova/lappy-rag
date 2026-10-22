package observability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultServiceName is used when no explicit tracing service name is configured.
	DefaultServiceName = "lappy-rag"
)

// OpenInferenceConfig configures OTLP/OpenInference tracing.
type OpenInferenceConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Endpoint    string `yaml:"endpoint"`
	ServiceName string `yaml:"service_name"`
}

type runtimeSettings struct {
	OpenInference OpenInferenceConfig `yaml:"openinference"`
}

// DetectOpenInferenceConfigPath finds the shared runtime settings file path.
func DetectOpenInferenceConfigPath() string {
	candidates := []string{
		"/app/configs/chat-settings.yaml",
		"../configs/chat-settings.yaml",
		"configs/chat-settings.yaml",
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	return ""
}

// LoadOpenInferenceConfigFromFile reads tracing config from the shared runtime settings file.
func LoadOpenInferenceConfigFromFile(path, defaultServiceName string) (OpenInferenceConfig, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return OpenInferenceConfig{}, fmt.Errorf("openinference config file path is required")
	}

	raw, err := os.ReadFile(filepath.Clean(trimmedPath)) //nolint:gosec // operator-configured path
	if err != nil {
		return OpenInferenceConfig{}, fmt.Errorf("read openinference config file: %w", err)
	}

	var settings runtimeSettings
	if err := yaml.Unmarshal(raw, &settings); err != nil {
		return OpenInferenceConfig{}, fmt.Errorf("parse openinference config file: %w", err)
	}

	cfg := settings.OpenInference
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = strings.TrimSpace(defaultServiceName)
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = DefaultServiceName
	}

	return cfg, nil
}

// ServiceNameFromConfig resolves the effective service name from config.
func ServiceNameFromConfig(cfg *OpenInferenceConfig) string {
	if cfg == nil || strings.TrimSpace(cfg.ServiceName) == "" {
		return DefaultServiceName
	}
	return strings.TrimSpace(cfg.ServiceName)
}

// WithOpenTelemetryTrace starts an operation span and returns its derived context.
func WithOpenTelemetryTrace(ctx context.Context, serviceName, operationName string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	resolvedServiceName := strings.TrimSpace(serviceName)
	if resolvedServiceName == "" {
		resolvedServiceName = DefaultServiceName
	}

	resolvedOperation := strings.TrimSpace(operationName)
	if resolvedOperation == "" {
		resolvedOperation = "operation"
	}

	tracer := otel.Tracer(resolvedServiceName)
	return tracer.Start(
		ctx,
		resolvedOperation,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("openinference.span.kind", "CHAIN"),
			attribute.String("command.operation", resolvedOperation),
			attribute.String("service.name", resolvedServiceName),
		),
	)
}

// EndSpanWithStatus records the final span status from the supplied error pointer.
func EndSpanWithStatus(span trace.Span, err *error) {
	if span == nil {
		return
	}

	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
		span.End()
		return
	}

	span.SetStatus(codes.Ok, "ok")
	span.End()
}
