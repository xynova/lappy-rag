package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/xynova/lappy-rag/internal/cli/shared"
	"github.com/xynova/lappy-rag/internal/mcpserver"
)

var version = "dev"

func main() {
	const purpose = "MCP server over SSE for documentation search"
	root := &cobra.Command{
		Use:          "mcp-server",
		Short:        purpose,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return shared.PrintAgentGuide(cmd.OutOrStdout(), "mcp-server", version, purpose)
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
		Short: "Run the MCP HTTP server",
		RunE: func(_ *cobra.Command, _ []string) error {
			return mcpserver.Run()
		},
	})

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
