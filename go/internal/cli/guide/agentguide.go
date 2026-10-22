package guide

import (
	"fmt"
	"io"
)

// PrintAgentGuide writes the agent operating guide for a CLI binary.
func PrintAgentGuide(w io.Writer, bin, version, purpose string) error {
	if _, err := fmt.Fprintln(w, bin, version); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, purpose); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Documentation for agents"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  AGENTS.md"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  ai-copilots/README.md"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Read-only"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  version, help"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Execute"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  serve (ingest-api, mcp-server)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  run (ingest-job)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Run", bin, "help for the full command catalog."); err != nil {
		return err
	}
	return nil
}
