package dspygo

import (
	"context"
	"testing"
	"time"
)

// C17 offline gate: rule-based Build is the default test path; live LLM replay is opt-in via DSPY_PLANNER_LIVE=1.
func TestBuildFallbackWithoutDeadline(t *testing.T) {
	p := NewPlanner("", "")
	plan := p.Build(context.Background(), "how do I configure ingest jobs")
	if len(plan.Queries) == 0 {
		t.Fatal("expected fallback queries")
	}
}

func TestBuildWithDeadlineUsesFallbackWhenNoLLM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := NewPlanner("", "")
	plan := p.Build(ctx, "mesh networking overview")
	if plan.Primary == "" {
		t.Fatal("expected primary query")
	}
}
