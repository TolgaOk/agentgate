package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:               "aga",
		Short:             "AgentGate Hub",
		Version:           "0.1.0-alpha",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	rootCmd.AddCommand(newAskCmd(), newMetricsCmd(), newAuthCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
