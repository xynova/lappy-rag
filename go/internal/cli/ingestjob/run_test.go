package ingestjob

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProjectsPathDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	got := DetectProjectsPath()
	if got != "../configs/bundles" {
		t.Fatalf("expected ../configs/bundles, got %q", got)
	}

	bundles := filepath.Join(dir, "configs", "bundles")
	if err := os.MkdirAll(bundles, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got = DetectProjectsPath()
	if got != "configs/bundles" {
		t.Fatalf("expected configs/bundles, got %q", got)
	}
}
