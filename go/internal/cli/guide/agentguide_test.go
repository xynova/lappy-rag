package guide

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintAgentGuideContainsHarnessPaths(t *testing.T) {
	var buf bytes.Buffer
	err := PrintAgentGuide(&buf, "ingest-api", "dev", "Ingest API and query planner HTTP service")
	if err != nil {
		t.Fatalf("PrintAgentGuide: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"ingest-api",
		"dev",
		"AGENTS.md",
		"ai-copilots/README.md",
		"version",
		"help",
		"serve",
		"run",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n") < 8 {
		t.Fatalf("expected multiple lines, got %d lines", strings.Count(out, "\n")+1)
	}
}
