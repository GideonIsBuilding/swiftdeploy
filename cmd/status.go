package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gideonisbuilding/swiftdeploy/internal"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Live-refreshing dashboard showing metrics and policy compliance",
	RunE:  runStatus,
}

// HistoryEntry is one scrape snapshot written to history.jsonl
type HistoryEntry struct {
	Timestamp        string   `json:"timestamp"`
	Mode             string   `json:"mode"`
	ReqPerSec        float64  `json:"req_per_sec"`
	P99LatencyMs     float64  `json:"p99_latency_ms"`
	ErrorRatePercent float64  `json:"error_rate_percent"`
	TotalRequests    float64  `json:"total_requests"`
	ChaosActive      float64  `json:"chaos_active"`
	InfraAllow       bool     `json:"infra_allow"`
	CanaryAllow      bool     `json:"canary_allow"`
	InfraViolations  []string `json:"infra_violations"`
	CanaryViolations []string `json:"canary_violations"`
}

const historyFile = "history.jsonl"
const refreshInterval = 3 * time.Second

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Open history.jsonl in append mode — every scrape adds one line
	f, err := os.OpenFile(historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", historyFile, err)
	}
	defer f.Close()

	// Handle Ctrl+C cleanly
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	// Track previous total requests to calculate req/s between scrapes
	var prevTotal float64
	var prevTime time.Time

	fmt.Println("📊 SwiftDeploy Status Dashboard — press Ctrl+C to exit")
	fmt.Println(strings.Repeat("─", 60))

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick
	runScrape(m, f, &prevTotal, &prevTime)

	for {
		select {
		case <-sig:
			fmt.Println("\n\n👋 Status dashboard stopped.")
			return nil
		case <-ticker.C:
			runScrape(m, f, &prevTotal, &prevTime)
		}
	}
}

func runScrape(m *internal.Manifest, f *os.File, prevTotal *float64, prevTime *time.Time) {
	now := time.Now()
	timestamp := now.UTC().Format(time.RFC3339)

	// ── Clear terminal and redraw ─────────────────────────────────────────────
	fmt.Print("\033[H\033[2J") // ANSI: move cursor home + clear screen
	fmt.Printf("📊 SwiftDeploy Status  %s\n", timestamp)
	fmt.Println(strings.Repeat("─", 60))

	// ── Scrape /metrics ───────────────────────────────────────────────────────
	raw, err := internal.ScrapeMetrics(m.Nginx.Port)
	if err != nil {
		fmt.Printf("⚠️  Cannot reach /metrics: %v\n", err)
		fmt.Println("   Is the stack running? Try: ./swiftdeploy deploy")
		return
	}

	sample, err := internal.ParseMetrics(raw)
	if err != nil {
		fmt.Printf("⚠️  Cannot parse metrics: %v\n", err)
		return
	}

	// ── Calculate req/s since last scrape ─────────────────────────────────────
	var reqPerSec float64
	if !prevTime.IsZero() && sample.TotalRequests >= *prevTotal {
		elapsed := now.Sub(*prevTime).Seconds()
		if elapsed > 0 {
			reqPerSec = (sample.TotalRequests - *prevTotal) / elapsed
		}
	}
	*prevTotal = sample.TotalRequests
	*prevTime = now

	// ── Parse state gauges from raw metrics ───────────────────────────────────
	appMode := parseGaugeValue(raw, "app_mode")
	chaosActive := parseGaugeValue(raw, "chaos_active")
	appUptime := parseGaugeValue(raw, "app_uptime_seconds")

	modeLabel := "stable"
	if appMode == 1 {
		modeLabel = "canary"
	}

	chaosLabel := chaosStateLabel(chaosActive)

	// ── Render metrics panel ──────────────────────────────────────────────────
	fmt.Println("  📈 METRICS")
	fmt.Printf("     Mode:          %s\n", highlight(modeLabel))
	fmt.Printf("     Uptime:        %.0fs\n", appUptime)
	fmt.Printf("     Req/s:         %.2f\n", reqPerSec)
	fmt.Printf("     P99 Latency:   %.2fms\n", sample.P99LatencyMs)
	fmt.Printf("     Error Rate:    %.4f%%\n", sample.ErrorRatePercent)
	fmt.Printf("     Total Req:     %.0f  (errors: %.0f)\n", sample.TotalRequests, sample.ErrorRequests)
	fmt.Printf("     Chaos:         %s\n", chaosLabel)
	fmt.Println()

	// ── Query OPA for live policy compliance ──────────────────────────────────
	fmt.Println("  🔒 POLICY COMPLIANCE")

	// Infrastructure policy
	var infraAllow bool
	var infraViolations []string
	stats, err := internal.CollectHostStats()
	if err != nil {
		fmt.Printf("     ⚠️  [infrastructure] cannot collect host stats: %v\n", err)
	} else {
		infraDecision, err := internal.QueryOPA(m.OPA.Port, "swiftdeploy/infrastructure/decision", map[string]any{
			"disk_free_gb":        stats.DiskFreeGB,
			"cpu_load":            stats.CPULoad,
			"memory_used_percent": stats.MemoryUsedPercent,
		})
		if err != nil {
			fmt.Printf("     ⚠️  [infrastructure] OPA unavailable: %v\n", err)
		} else {
			infraAllow = infraDecision.Allow
			infraViolations = infraDecision.Violations
			renderPolicyRow("infrastructure", infraDecision)
		}
	}

	// Canary policy
	var canaryAllow bool
	var canaryViolations []string
	canaryDecision, err := internal.QueryOPA(m.OPA.Port, "swiftdeploy/canary/decision", map[string]any{
		"error_rate_percent": sample.ErrorRatePercent,
		"p99_latency_ms":     sample.P99LatencyMs,
		"sample_window_secs": sample.SampleWindowSecs,
		"total_requests":     sample.TotalRequests,
		"error_requests":     sample.ErrorRequests,
	})
	if err != nil {
		fmt.Printf("     ⚠️  [canary] OPA unavailable: %v\n", err)
	} else {
		canaryAllow = canaryDecision.Allow
		canaryViolations = canaryDecision.Violations
		renderPolicyRow("canary", canaryDecision)
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Refreshing every %s — Ctrl+C to exit\n", refreshInterval)
	fmt.Printf("  Audit trail: %s\n", historyFile)

	// ── Append to history.jsonl ───────────────────────────────────────────────
	entry := HistoryEntry{
		Timestamp:        timestamp,
		Mode:             modeLabel,
		ReqPerSec:        reqPerSec,
		P99LatencyMs:     sample.P99LatencyMs,
		ErrorRatePercent: sample.ErrorRatePercent,
		TotalRequests:    sample.TotalRequests,
		ChaosActive:      chaosActive,
		InfraAllow:       infraAllow,
		CanaryAllow:      canaryAllow,
		InfraViolations:  infraViolations,
		CanaryViolations: canaryViolations,
	}

	line, err := json.Marshal(entry)
	if err == nil {
		f.WriteString(string(line) + "\n")
	}
}

// ── Rendering helpers ─────────────────────────────────────────────────────────

func renderPolicyRow(name string, d *internal.OPADecision) {
	if d.Allow {
		fmt.Printf("     ✅ PASS  [%s]\n", name)
		return
	}
	fmt.Printf("     ❌ FAIL  [%s]\n", name)
	for _, v := range d.Violations {
		fmt.Printf("              → %s\n", v)
	}
}

func highlight(s string) string {
	if s == "canary" {
		return "⚡ canary"
	}
	return "🟢 stable"
}

func chaosStateLabel(v float64) string {
	switch v {
	case 1:
		return "⚠️  slow"
	case 2:
		return "🔴 error injection"
	default:
		return "✅ none"
	}
}

// parseGaugeValue extracts the float value of a gauge metric by name.
// e.g. parseGaugeValue(raw, "app_mode") → 1.0
func parseGaugeValue(raw, metricName string) float64 {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		// Match exact metric name (not a prefix of another metric)
		if strings.HasPrefix(line, metricName+" ") || strings.HasPrefix(line, metricName+"{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var v float64
				fmt.Sscanf(parts[len(parts)-1], "%f", &v)
				return v
			}
		}
	}
	return 0
}
