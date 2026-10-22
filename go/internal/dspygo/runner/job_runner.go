package runner

import (
	"context"
	"fmt"
	"strings"

	dspyregistry "github.com/xynova/lappy-rag/internal/dspygo/registry"
	dspytracing "github.com/xynova/lappy-rag/internal/dspygo/tracing"
)

// GeneratorInput defines the input contract for generation modules.
type GeneratorInput interface {
	ToMap() map[string]interface{}
	GetVersion() int
}

// GenerationConfig controls generation execution for one job.
type GenerationConfig struct {
	JobName string
	Module  dspytracing.ModuleInfo
}

// JobRunner executes registered generators and evaluator workflows.
type JobRunner struct {
	registry    *dspyregistry.ModuleRegistry
	interceptor *dspytracing.OpenInferenceInterceptor
}

// NewJobRunner creates a JobRunner.
func NewJobRunner(registry *dspyregistry.ModuleRegistry, interceptor *dspytracing.OpenInferenceInterceptor) *JobRunner {
	if registry == nil {
		panic("module registry cannot be nil")
	}

	return &JobRunner{registry: registry, interceptor: interceptor}
}

// Generate executes a generator job.
func (r *JobRunner) Generate(ctx context.Context, cfg GenerationConfig, input GeneratorInput) (map[string]interface{}, error) {
	if input == nil {
		return nil, fmt.Errorf("generator input cannot be nil")
	}

	jobName := strings.TrimSpace(cfg.JobName)
	if jobName == "" {
		return nil, fmt.Errorf("job name is required")
	}

	generator, err := r.registry.Generator(jobName)
	if err != nil {
		return nil, fmt.Errorf("resolve generator %q: %w", jobName, err)
	}

	handler := func(runCtx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
		return generator.Generate(runCtx, payload)
	}
	if r.interceptor != nil {
		handler = r.interceptor.Wrap(cfg.Module, handler)
	}

	output, err := handler(ctx, input.ToMap())
	if err != nil {
		return nil, fmt.Errorf("generate job %q: %w", jobName, err)
	}

	return output, nil
}

// EvaluateWorkflow executes a registered evaluator workflow.
func (r *JobRunner) EvaluateWorkflow(ctx context.Context, jobName string, evalInputs map[string]interface{}) (*dspyregistry.AggregatedEvaluation, error) {
	trimmedJobName := strings.TrimSpace(jobName)
	if trimmedJobName == "" {
		return nil, fmt.Errorf("job name is required")
	}

	workflow, err := r.registry.Workflow(trimmedJobName)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow %q: %w", trimmedJobName, err)
	}

	evaluation, err := workflow.Evaluate(ctx, evalInputs)
	if err != nil {
		return nil, fmt.Errorf("evaluate workflow %q: %w", trimmedJobName, err)
	}

	return evaluation, nil
}

// BuildEvaluationInputs composes standard evaluation payload fields.
func BuildEvaluationInputs(generatorInput, generatorOutput map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, 3)
	out["generator_input"] = copyMap(generatorInput)
	out["generator_output"] = copyMap(generatorOutput)

	version := 0
	if rawVersion, ok := generatorInput["version"]; ok {
		switch typed := rawVersion.(type) {
		case int:
			version = typed
		case int32:
			version = int(typed)
		case int64:
			version = int(typed)
		case float64:
			version = int(typed)
		}
	}
	out["iteration_version"] = version

	return out
}

func copyMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return map[string]interface{}{}
	}

	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
