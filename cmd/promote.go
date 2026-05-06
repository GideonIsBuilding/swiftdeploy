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

	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// ── OPA pre-promote gate (canary only) ────────────────────────────────────
	// Promoting to stable is always safe — no policy check needed.
	// Promoting to canary requires the current app metrics to be healthy.
	if mode == "canary" {
		fmt.Println("🔒 Running pre-promote policy checks...")

		// Scrape /metrics via nginx port (goes through the proxy as real traffic would)
		metricsURL := fmt.Sprintf("http://localhost:%d/metrics", m.Nginx.Port)
		fmt.Printf("   scraping %s ...\n", metricsURL)

		raw, err := internal.ScrapeMetrics(m.Nginx.Port)
		if err != nil {
			return fmt.Errorf("❌ Cannot scrape metrics: %w\n   Is the stack running? Try: ./swiftdeploy deploy", err)
		}

		sample, err := internal.ParseMetrics(raw)
		if err != nil {
			return fmt.Errorf("❌ Cannot parse metrics: %w", err)
		}

		fmt.Printf("   error_rate:    %.4f%%  (max: 1.00%%)\n", sample.ErrorRatePercent)
		fmt.Printf("   p99_latency:   %.2fms  (max: 500ms)\n", sample.P99LatencyMs)
		fmt.Printf("   total_requests: %.0f  errors: %.0f\n\n", sample.TotalRequests, sample.ErrorRequests)

		decision, err := internal.QueryOPA(m.OPA.Port, "swiftdeploy/canary/decision", map[string]any{
			"error_rate_percent": sample.ErrorRatePercent,
			"p99_latency_ms":     sample.P99LatencyMs,
			"sample_window_secs": sample.SampleWindowSecs,
			"total_requests":     sample.TotalRequests,
			"error_requests":     sample.ErrorRequests,
		})
		if err != nil {
			return fmt.Errorf("❌ Policy check failed: %w\n   Promote aborted.", err)
		}

		internal.PrintDecision("canary", decision)

		if !decision.Allow {
			return fmt.Errorf("❌ Promotion blocked by policy — fix violations above and retry")
		}

		fmt.Println()
	}

	// ── Step 1: update mode in manifest.yaml in-place ────────────────────────
	fmt.Printf("🔄 Promoting to %s mode...\n", mode)
	if err := internal.UpdateMode("manifest.yaml", mode); err != nil {
		return fmt.Errorf("updating manifest: %w", err)
	}
	fmt.Printf("✅ manifest.yaml updated — mode: %s\n", mode)

	// ── Step 2: regenerate docker-compose.yml with new MODE env var ───────────
	fmt.Println("⚙️  Regenerating configs...")
	if err := generateConfigs(); err != nil {
		return err
	}

	// ── Step 3: force-recreate only the app container ────────────────────────
	// "docker compose restart" does NOT apply env var changes.
	// "up --force-recreate" tears down and recreates with the new MODE value.
	fmt.Println("🔁 Recreating app container with new mode...")
	recreate := exec.Command("docker", "compose", "up", "-d", "--force-recreate", "--no-deps", "app")
	recreate.Stdout = os.Stdout
	recreate.Stderr = os.Stderr
	if err := recreate.Run(); err != nil {
		return fmt.Errorf("docker compose up --force-recreate app failed: %w", err)
	}

	// ── Step 4: confirm new mode via /healthz ─────────────────────────────────
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
