package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xynova/lappy-rag/internal/cli/ingestjob"
	"github.com/xynova/lappy-rag/internal/cli/shared"
)

var version = "dev"

func main() {
	const purpose = "One-shot ingest job for a project bundle"
	root := &cobra.Command{
		Use:          "ingest-job",
		Short:        purpose,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return shared.PrintAgentGuide(cmd.OutOrStdout(), "ingest-job", version, purpose)
		},
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(version)
		},
	})

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run ingest for one project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := ingestjob.RunConfig{
				ProjectID:    strings.TrimSpace(cmd.Flag("project").Value.String()),
				Mode:         strings.TrimSpace(cmd.Flag("mode").Value.String()),
				ProjectsPath: strings.TrimSpace(cmd.Flag("projects").Value.String()),
			}
			return cfg.Run(cmd.Context())
		},
	}
	runCmd.Flags().String("project", "", "project id")
	runCmd.Flags().String("mode", "incremental", "mode: full|incremental|watch")
	runCmd.Flags().String("projects", ingestjob.DetectProjectsPath(), "comma-separated list of project bundle directories")
	_ = runCmd.MarkFlagRequired("project")
	root.AddCommand(runCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
