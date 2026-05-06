package internal

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MetricsSample holds the calculated values extracted from /metrics
// that get sent to OPA's canary policy.
type MetricsSample struct {
	ErrorRatePercent float64 `json:"error_rate_percent"`
	P99LatencyMs     float64 `json:"p99_latency_ms"`
	SampleWindowSecs int     `json:"sample_window_secs"`
	TotalRequests    float64 `json:"total_requests"`
	ErrorRequests    float64 `json:"error_requests"`
}

// ScrapeMetrics fetches raw Prometheus text from the app's /metrics endpoint.
func ScrapeMetrics(appPort int) (string, error) {
	url := fmt.Sprintf("http://localhost:%d/metrics", appPort)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("scraping /metrics at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/metrics returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading /metrics response: %w", err)
	}
	return string(body), nil
}

// ParseMetrics calculates error rate and P99 latency from raw Prometheus text.
// It reads:
//   - http_requests_total{status_code=~"5.."} for error count
//   - http_requests_total (all) for total count
//   - http_request_duration_seconds_bucket for P99 via histogram interpolation
func ParseMetrics(raw string) (*MetricsSample, error) {
	var totalRequests float64
	var errorRequests float64

	// Histogram buckets: map[le_value]cumulative_count
	// le = "less than or equal" upper bound in seconds
	buckets := map[float64]float64{}
	var histogramCount float64

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// ── http_requests_total ──────────────────────────────────────────────
		if strings.HasPrefix(line, "http_requests_total{") {
			val, err := parseLineValue(line)
			if err != nil {
				continue
			}

			// Only count non-/metrics paths to avoid noise
			if strings.Contains(line, `path="/metrics"`) {
				continue
			}

			totalRequests += val

			// Count 5xx responses as errors
			if is5xx(line) {
				errorRequests += val
			}
			continue
		}

		// ── http_request_duration_seconds_bucket ─────────────────────────────
		if strings.HasPrefix(line, "http_request_duration_seconds_bucket{") {
			le, val, err := parseBucketLine(line)
			if err != nil {
				continue
			}
			if le == "+Inf" {
				histogramCount = val
				continue
			}
			leF, err := strconv.ParseFloat(le, 64)
			if err != nil {
				continue
			}
			buckets[leF] += val
		}
	}

	// Calculate error rate
	var errorRatePercent float64
	if totalRequests > 0 {
		errorRatePercent = (errorRequests / totalRequests) * 100
	}

	// Calculate P99 from histogram buckets
	p99LatencyMs := calculateP99(buckets, histogramCount)

	return &MetricsSample{
		ErrorRatePercent: errorRatePercent,
		P99LatencyMs:     p99LatencyMs,
		SampleWindowSecs: 30,
		TotalRequests:    totalRequests,
		ErrorRequests:    errorRequests,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseLineValue extracts the float value from a Prometheus metric line.
// e.g. http_requests_total{...} 42  →  42.0
func parseLineValue(line string) (float64, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, fmt.Errorf("unexpected line format: %s", line)
	}
	return strconv.ParseFloat(parts[len(parts)-1], 64)
}

// is5xx returns true if the metric line has a 5xx status_code label.
func is5xx(line string) bool {
	for _, code := range []string{
		`status_code="500"`, `status_code="501"`, `status_code="502"`,
		`status_code="503"`, `status_code="504"`, `status_code="505"`,
	} {
		if strings.Contains(line, code) {
			return true
		}
	}
	return false
}

// parseBucketLine extracts le and count from a histogram bucket line.
// e.g. http_request_duration_seconds_bucket{le="0.1"} 5  →  "0.1", 5.0
func parseBucketLine(line string) (string, float64, error) {
	// Extract le="..." value
	leStart := strings.Index(line, `le="`)
	if leStart == -1 {
		return "", 0, fmt.Errorf("no le label in: %s", line)
	}
	leStart += 4
	leEnd := strings.Index(line[leStart:], `"`)
	if leEnd == -1 {
		return "", 0, fmt.Errorf("unclosed le label in: %s", line)
	}
	le := line[leStart : leStart+leEnd]

	val, err := parseLineValue(line)
	if err != nil {
		return "", 0, err
	}
	return le, val, nil
}

// calculateP99 interpolates P99 latency in milliseconds from histogram buckets.
// Uses linear interpolation within the bucket that contains the 99th percentile.
func calculateP99(buckets map[float64]float64, totalCount float64) float64 {
	if totalCount == 0 || len(buckets) == 0 {
		return 0
	}

	target := totalCount * 0.99

	// Sort bucket upper bounds ascending
	bounds := make([]float64, 0, len(buckets))
	for le := range buckets {
		bounds = append(bounds, le)
	}
	sortFloat64s(bounds)

	var prevBound, prevCount float64
	for _, le := range bounds {
		count := buckets[le]
		if count >= target {
			// Interpolate within this bucket
			if count == prevCount {
				return le * 1000
			}
			fraction := (target - prevCount) / (count - prevCount)
			interpolated := prevBound + fraction*(le-prevBound)
			return interpolated * 1000 // convert seconds → milliseconds
		}
		prevBound = le
		prevCount = count
	}

	// All observations are in the +Inf bucket
	return prevBound * 1000
}

// sortFloat64s sorts a float64 slice in ascending order.
func sortFloat64s(s []float64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
