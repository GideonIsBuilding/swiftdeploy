package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var cleanFlag bool

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Remove all containers, networks, and volumes",
	RunE:  runTeardown,
}

func init() {
	teardownCmd.Flags().BoolVar(&cleanFlag, "clean", false, "Also delete generated nginx.conf and docker-compose.yml")
}

func runTeardown(cmd *cobra.Command, args []string) error {
	fmt.Println("🛑 Tearing down the stack...")

	down := exec.Command("docker", "compose", "down", "--volumes", "--remove-orphans")
	down.Stdout = os.Stdout
	down.Stderr = os.Stderr
	if err := down.Run(); err != nil {
		return fmt.Errorf("docker compose down failed: %w", err)
	}
	fmt.Println("✅ Containers, networks, and volumes removed.")

	if cleanFlag {
		for _, f := range []string{"nginx.conf", "docker-compose.yml"} {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				fmt.Printf("⚠️  Could not remove %s: %v\n", f, err)
			} else {
				fmt.Printf("🗑️  Deleted %s\n", f)
			}
		}
	}

	fmt.Println("✅ Teardown complete.")
	return nil
}
