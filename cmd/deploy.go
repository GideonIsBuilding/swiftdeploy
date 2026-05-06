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
	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// ── Step 1: generate configs ──────────────────────────────────────────────
	fmt.Println("⚙️  Generating configs...")
	if err := generateConfigs(); err != nil {
		return err
	}

	// ── Step 2: start OPA first so policy checks can run ─────────────────────
	// OPA must be up before we can query it. We bring it up in isolation,
	// wait until its API responds, then run the policy gate.
	fmt.Println("\n🔐 Starting OPA policy engine...")
	opaUp := exec.Command("docker", "compose", "up", "-d", "opa")
	opaUp.Stdout = os.Stdout
	opaUp.Stderr = os.Stderr
	if err := opaUp.Run(); err != nil {
		return fmt.Errorf("failed to start OPA: %w", err)
	}

	if err := waitForOPA(m.OPA.Port); err != nil {
		return err
	}

	// ── Step 3: OPA pre-deploy gate ───────────────────────────────────────────
	fmt.Println("\n🔒 Running pre-deploy policy checks...")

	stats, err := internal.CollectHostStats()
	if err != nil {
		return fmt.Errorf("collecting host stats: %w", err)
	}

	fmt.Printf("   disk_free:    %.2f GB\n", stats.DiskFreeGB)
	fmt.Printf("   cpu_load:     %.2f\n", stats.CPULoad)
	fmt.Printf("   memory_used:  %.2f%%\n\n", stats.MemoryUsedPercent)

	decision, err := internal.QueryOPA(m.OPA.Port, "swiftdeploy/infrastructure/decision", map[string]any{
		"disk_free_gb":        stats.DiskFreeGB,
		"cpu_load":            stats.CPULoad,
		"memory_used_percent": stats.MemoryUsedPercent,
	})
	if err != nil {
		return fmt.Errorf("❌ Policy check failed: %w\n   Deploy aborted.", err)
	}

	internal.PrintDecision("infrastructure", decision)

	if !decision.Allow {
		return fmt.Errorf("❌ Deploy blocked by policy — fix violations above and retry")
	}

	fmt.Println()

	// ── Step 4: bring up the rest of the stack ────────────────────────────────
	fmt.Println("🚀 Starting stack...")
	up := exec.Command("docker", "compose", "up", "-d", "--remove-orphans")
	up.Stdout = os.Stdout
	up.Stderr = os.Stderr
	if err := up.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	// ── Step 5: poll /healthz until healthy or 60s timeout ───────────────────
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

// waitForOPA polls OPA's health endpoint until it responds or times out.
func waitForOPA(port int) error {
	healthURL := fmt.Sprintf("http://localhost:%d/health", port)
	fmt.Printf("⏳ Waiting for OPA at %s ...\n", healthURL)

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Println("✅ OPA is ready.")
			return nil
		}
		if err == nil {
			resp.Body.Close()
		}
		fmt.Print(".")
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("❌ OPA did not become ready within 30s — check: docker compose logs opa")
}
