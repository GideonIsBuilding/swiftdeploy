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

var promoteCmd = &cobra.Command{
	Use:   "promote [canary|stable]",
	Short: "Switch deployment mode with a rolling service restart",
	Args:  cobra.ExactArgs(1),
	RunE:  runPromote,
}

func runPromote(cmd *cobra.Command, args []string) error {
	mode := args[0]
	if mode != "canary" && mode != "stable" {
		return fmt.Errorf("mode must be 'canary' or 'stable', got: %q", mode)
	}

	// Step 1: update mode in manifest.yaml in-place
	fmt.Printf("🔄 Promoting to %s mode...\n", mode)
	if err := internal.UpdateMode("manifest.yaml", mode); err != nil {
		return fmt.Errorf("updating manifest: %w", err)
	}
	fmt.Printf("✅ manifest.yaml updated — mode: %s\n", mode)

	// Step 2: regenerate docker-compose.yml with new MODE env var
	fmt.Println("⚙️  Regenerating docker-compose.yml...")
	if err := generateConfigs(); err != nil {
		return err
	}

	// Step 3: recreate only the app container so new MODE env var takes effect
	// NOTE: "docker compose restart" does NOT apply env var changes from docker-compose.yml.
	//       Only "up --force-recreate" picks up the updated config.
	fmt.Println("🔁 Recreating app container with new mode...")
	recreate := exec.Command("docker", "compose", "up", "-d", "--force-recreate", "--no-deps", "app")
	recreate.Stdout = os.Stdout
	recreate.Stderr = os.Stderr
	if err := recreate.Run(); err != nil {
		return fmt.Errorf("docker compose up --force-recreate app failed: %w", err)
	}

	// Step 4: confirm new mode via /healthz
	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("reloading manifest: %w", err)
	}

	healthURL := fmt.Sprintf("http://localhost:%d/healthz", m.Nginx.Port)
	fmt.Printf("⏳ Waiting for app to be healthy at %s ...\n", healthURL)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			xMode := resp.Header.Get("X-Mode")
			resp.Body.Close()

			if mode == "canary" && xMode != "canary" {
				fmt.Print(".")
				time.Sleep(2 * time.Second)
				continue
			}
			fmt.Printf("\n✅ Promotion complete! App is running in %s mode.\n", mode)
			if xMode != "" {
				fmt.Printf("   X-Mode header: %s\n", xMode)
			}
			return nil
		}
		if err == nil {
			resp.Body.Close()
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("❌ app did not become healthy after promotion — check: docker compose logs app")
}
