package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Generate audit_report.md from history.jsonl",
	RunE:  runAudit,
}

// auditEntry mirrors HistoryEntry from status.go — parsed from history.jsonl
type auditEntry struct {
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

const reportFile = "audit_report.md"

func runAudit(cmd *cobra.Command, args []string) error {
	// ── Read history.jsonl ────────────────────────────────────────────────────
	f, err := os.Open(historyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("history.jsonl not found — run: ./swiftdeploy status first")
		}
		return fmt.Errorf("opening %s: %w", historyFile, err)
	}
	defer f.Close()

	var entries []auditEntry
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e auditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			fmt.Printf("⚠️  Skipping malformed line %d: %v\n", lineNum, err)
			continue
		}
		entries = append(entries, e)
	}

	if len(entries) == 0 {
		return fmt.Errorf("no valid entries in %s — run: ./swiftdeploy status to collect data", historyFile)
	}

	fmt.Printf("📖 Parsed %d entries from %s\n", len(entries), historyFile)

	// ── Build report ──────────────────────────────────────────────────────────
	var sb strings.Builder

	writeHeader(&sb, entries)
	writeSummary(&sb, entries)
	writeTimeline(&sb, entries)
	writeViolations(&sb, entries)
	writeMetricsSummary(&sb, entries)
	writeFooter(&sb)

	// ── Write audit_report.md ─────────────────────────────────────────────────
	if err := os.WriteFile(reportFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", reportFile, err)
	}

	fmt.Printf("✅ Report written to %s\n", reportFile)
	fmt.Printf("   Entries: %d\n", len(entries))
	fmt.Printf("   Violations: %d\n", countViolations(entries))
	fmt.Printf("   Mode changes: %d\n", countModeChanges(entries))
	return nil
}

// ── Section writers ───────────────────────────────────────────────────────────

func writeHeader(sb *strings.Builder, entries []auditEntry) {
	first := entries[0].Timestamp
	last := entries[len(entries)-1].Timestamp
	sb.WriteString("# SwiftDeploy Audit Report\n\n")
	sb.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Period:** %s → %s  \n", first, last))
	sb.WriteString(fmt.Sprintf("**Total Scrapes:** %d  \n\n", len(entries)))
	sb.WriteString("---\n\n")
}

func writeSummary(sb *strings.Builder, entries []auditEntry) {
	violations := countViolations(entries)
	modeChanges := countModeChanges(entries)
	chaosEvents := countChaosEvents(entries)

	violationIcon := "✅"
	if violations > 0 {
		violationIcon = "⚠️"
	}

	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|--------|-------|\n")
	sb.WriteString(fmt.Sprintf("| Total scrapes | %d |\n", len(entries)))
	sb.WriteString(fmt.Sprintf("| Mode changes | %d |\n", modeChanges))
	sb.WriteString(fmt.Sprintf("| Chaos events | %d |\n", chaosEvents))
	sb.WriteString(fmt.Sprintf("| Policy violations | %s %d |\n", violationIcon, violations))
	sb.WriteString(fmt.Sprintf("| Final mode | `%s` |\n", entries[len(entries)-1].Mode))
	sb.WriteString("\n---\n\n")
}

func writeTimeline(sb *strings.Builder, entries []auditEntry) {
	sb.WriteString("## Timeline\n\n")
	sb.WriteString("Significant events: mode changes and chaos injections.\n\n")
	sb.WriteString("| Timestamp | Event | Details |\n")
	sb.WriteString("|-----------|-------|----------|\n")

	prevMode := ""
	prevChaos := -1.0

	for _, e := range entries {
		// Mode change
		if e.Mode != prevMode && prevMode != "" {
			sb.WriteString(fmt.Sprintf("| `%s` | 🔄 Mode change | `%s` → `%s` |\n",
				e.Timestamp, prevMode, e.Mode))
		}
		if prevMode == "" {
			sb.WriteString(fmt.Sprintf("| `%s` | 🚀 Monitoring started | mode: `%s` |\n",
				e.Timestamp, e.Mode))
		}

		// Chaos state change
		if e.ChaosActive != prevChaos && prevChaos != -1 {
			if e.ChaosActive == 0 {
				sb.WriteString(fmt.Sprintf("| `%s` | ✅ Chaos recovered | chaos cleared |\n",
					e.Timestamp))
			} else {
				sb.WriteString(fmt.Sprintf("| `%s` | ⚠️ Chaos injected | %s |\n",
					e.Timestamp, chaosEventLabel(e.ChaosActive)))
			}
		}

		prevMode = e.Mode
		prevChaos = e.ChaosActive
	}

	sb.WriteString("\n---\n\n")
}

func writeViolations(sb *strings.Builder, entries []auditEntry) {
	sb.WriteString("## Policy Violations\n\n")

	// Collect all violation events
	type violation struct {
		timestamp string
		domain    string
		messages  []string
	}
	var violations []violation

	for _, e := range entries {
		if len(e.InfraViolations) > 0 {
			violations = append(violations, violation{
				timestamp: e.Timestamp,
				domain:    "infrastructure",
				messages:  e.InfraViolations,
			})
		}
		if len(e.CanaryViolations) > 0 {
			violations = append(violations, violation{
				timestamp: e.Timestamp,
				domain:    "canary",
				messages:  e.CanaryViolations,
			})
		}
	}

	if len(violations) == 0 {
		sb.WriteString("✅ No policy violations recorded during this period.\n\n")
		sb.WriteString("---\n\n")
		return
	}

	sb.WriteString(fmt.Sprintf("⚠️ **%d violation(s) recorded:**\n\n", len(violations)))
	sb.WriteString("| Timestamp | Domain | Violation |\n")
	sb.WriteString("|-----------|--------|----------|\n")

	for _, v := range violations {
		for i, msg := range v.messages {
			if i == 0 {
				sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n",
					v.timestamp, v.domain, msg))
			} else {
				// Multi-violation: continue rows without repeating timestamp
				sb.WriteString(fmt.Sprintf("| | | %s |\n", msg))
			}
		}
	}

	sb.WriteString("\n---\n\n")
}

func writeMetricsSummary(sb *strings.Builder, entries []auditEntry) {
	// Calculate averages and peaks
	var totalReqPerSec, totalP99, totalErrorRate float64
	var peakReqPerSec, peakP99, peakErrorRate float64

	for _, e := range entries {
		totalReqPerSec += e.ReqPerSec
		totalP99 += e.P99LatencyMs
		totalErrorRate += e.ErrorRatePercent

		if e.ReqPerSec > peakReqPerSec {
			peakReqPerSec = e.ReqPerSec
		}
		if e.P99LatencyMs > peakP99 {
			peakP99 = e.P99LatencyMs
		}
		if e.ErrorRatePercent > peakErrorRate {
			peakErrorRate = e.ErrorRatePercent
		}
	}

	n := float64(len(entries))
	sb.WriteString("## Metrics Summary\n\n")
	sb.WriteString("| Metric | Average | Peak |\n")
	sb.WriteString("|--------|---------|------|\n")
	sb.WriteString(fmt.Sprintf("| Req/s | %.2f | %.2f |\n", totalReqPerSec/n, peakReqPerSec))
	sb.WriteString(fmt.Sprintf("| P99 Latency (ms) | %.2f | %.2f |\n", totalP99/n, peakP99))
	sb.WriteString(fmt.Sprintf("| Error Rate (%%) | %.4f | %.4f |\n", totalErrorRate/n, peakErrorRate))
	sb.WriteString("\n---\n\n")
}

func writeFooter(sb *strings.Builder) {
	sb.WriteString("## About\n\n")
	sb.WriteString("Generated by **SwiftDeploy** — declarative container deployment.\n\n")
	sb.WriteString(fmt.Sprintf("_Report generated at %s_\n", time.Now().UTC().Format(time.RFC3339)))
}

// ── Counters ──────────────────────────────────────────────────────────────────

func countViolations(entries []auditEntry) int {
	count := 0
	for _, e := range entries {
		count += len(e.InfraViolations)
		count += len(e.CanaryViolations)
	}
	return count
}

func countModeChanges(entries []auditEntry) int {
	count := 0
	prev := ""
	for _, e := range entries {
		if prev != "" && e.Mode != prev {
			count++
		}
		prev = e.Mode
	}
	return count
}

func countChaosEvents(entries []auditEntry) int {
	count := 0
	prev := -1.0
	for _, e := range entries {
		if prev != -1 && e.ChaosActive != 0 && e.ChaosActive != prev {
			count++
		}
		prev = e.ChaosActive
	}
	return count
}

func chaosEventLabel(v float64) string {
	switch v {
	case 1:
		return "slow mode activated"
	case 2:
		return "error injection activated"
	default:
		return "unknown chaos state"
	}
}
