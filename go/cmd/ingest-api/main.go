package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/xynova/lappy-rag/internal/cli/shared"
	"github.com/xynova/lappy-rag/internal/ingestapi"
)

var version = "dev"

func main() {
	const purpose = "Ingest API and query planner HTTP service"
	root := &cobra.Command{
		Use:          "ingest-api",
		Short:        purpose,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return shared.PrintAgentGuide(cmd.OutOrStdout(), "ingest-api", version, purpose)
		},
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(version)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		RunE: func(_ *cobra.Command, _ []string) error {
			return ingestapi.Run()
		},
	})

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
