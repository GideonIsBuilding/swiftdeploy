package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "swiftdeploy",
	Short: "SwiftDeploy — declarative container deployment from a manifest",
	Long: `SwiftDeploy reads manifest.yaml and manages your entire stack:
  generates Nginx and Docker Compose configs, deploys containers,
  switches deployment modes, and tears everything down cleanly.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(promoteCmd)
	rootCmd.AddCommand(teardownCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(auditCmd)
}
