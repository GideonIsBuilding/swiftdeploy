package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gideonisbuilding/swiftdeploy/internal"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Generate configs, bring up the stack, and wait until healthy",
	RunE:  runDeploy,
}

func runDeploy(cmd *cobra.Command, args []string) error {
	// Step 1: generate configs
	fmt.Println("⚙️  Generating configs...")
	if err := generateConfigs(); err != nil {
		return err
	}

	// Step 2: load manifest to know the nginx port for health polling
	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Step 3: bring up the stack
	fmt.Println("\n🚀 Starting stack...")
	up := exec.Command("docker", "compose", "up", "-d", "--remove-orphans")
	up.Stdout = os.Stdout
	up.Stderr = os.Stderr
	if err := up.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	// Step 4: poll /healthz until healthy or 60s timeout
	healthURL := fmt.Sprintf("http://localhost:%d/healthz", m.Nginx.Port)
	fmt.Printf("\n⏳ Waiting for health check at %s ...\n", healthURL)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Println("✅ Stack is healthy and ready!")
			fmt.Printf("   → App running at http://localhost:%d\n", m.Nginx.Port)
			return nil
		}
		if err == nil {
			resp.Body.Close()
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("❌ health check timed out after 60s — check: docker compose logs")
}
