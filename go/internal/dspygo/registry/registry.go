package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Generator executes a generation module for a job.
type Generator interface {
	Generate(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)
}

// AggregatedEvaluation is the normalized evaluation output for a job iteration.
type AggregatedEvaluation struct {
	Score    float64                `json:"score"`
	Feedback string                 `json:"feedback"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

// EvaluatorWorkflow executes a chained evaluator workflow.
type EvaluatorWorkflow interface {
	Evaluate(ctx context.Context, inputs map[string]interface{}) (*AggregatedEvaluation, error)
}

// ModuleRegistry stores generators and evaluator workflows keyed by job name.
type ModuleRegistry struct {
	mu         sync.RWMutex
	generators map[string]Generator
	workflows  map[string]EvaluatorWorkflow
}

// NewModuleRegistry creates an empty module registry.
func NewModuleRegistry() *ModuleRegistry {
	return &ModuleRegistry{
		generators: make(map[string]Generator),
		workflows:  make(map[string]EvaluatorWorkflow),
	}
}

// RegisterGenerator registers a generator for a job.
func (r *ModuleRegistry) RegisterGenerator(jobName string, generator Generator) error {
	if r == nil {
		return fmt.Errorf("module registry is nil")
	}
	if generator == nil {
		return fmt.Errorf("generator for job %q cannot be nil", strings.TrimSpace(jobName))
	}

	key := strings.TrimSpace(jobName)
	if key == "" {
		return fmt.Errorf("job name is required for generator registration")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.generators[key] = generator
	return nil
}

// RegisterWorkflow registers an evaluator workflow for a job.
func (r *ModuleRegistry) RegisterWorkflow(jobName string, workflow EvaluatorWorkflow) error {
	if r == nil {
		return fmt.Errorf("module registry is nil")
	}
	if workflow == nil {
		return fmt.Errorf("workflow for job %q cannot be nil", strings.TrimSpace(jobName))
	}

	key := strings.TrimSpace(jobName)
	if key == "" {
		return fmt.Errorf("job name is required for workflow registration")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.workflows[key] = workflow
	return nil
}

// Generator resolves a registered generator for a job.
func (r *ModuleRegistry) Generator(jobName string) (Generator, error) {
	if r == nil {
		return nil, fmt.Errorf("module registry is nil")
	}

	key := strings.TrimSpace(jobName)
	if key == "" {
		return nil, fmt.Errorf("job name is required")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	generator, ok := r.generators[key]
	if !ok {
		return nil, fmt.Errorf("generator %q not registered", key)
	}

	return generator, nil
}

// Workflow resolves a registered evaluator workflow for a job.
func (r *ModuleRegistry) Workflow(jobName string) (EvaluatorWorkflow, error) {
	if r == nil {
		return nil, fmt.Errorf("module registry is nil")
	}

	key := strings.TrimSpace(jobName)
	if key == "" {
		return nil, fmt.Errorf("job name is required")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	workflow, ok := r.workflows[key]
	if !ok {
		return nil, fmt.Errorf("workflow %q not registered", key)
	}

	return workflow, nil
}
