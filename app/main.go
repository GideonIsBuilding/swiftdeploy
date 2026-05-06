package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ── Process start time ────────────────────────────────────────────────────────

var startTime = time.Now()

// ── Prometheus metrics ────────────────────────────────────────────────────────

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, path, and status code.",
		},
		[]string{"method", "path", "status_code"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets, // .005 .01 .025 .05 .1 .25 .5 1 2.5 5 10
		},
		[]string{"method", "path"},
	)

	appUptimeSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "app_uptime_seconds",
		Help: "Seconds since the app process started.",
	})

	// 0 = stable, 1 = canary
	appModeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "app_mode",
		Help: "Current deployment mode: 0=stable, 1=canary.",
	})

	// 0 = none, 1 = slow, 2 = error
	chaosActiveGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "chaos_active",
		Help: "Active chaos mode: 0=none, 1=slow, 2=error.",
	})
)

// ── Chaos state ───────────────────────────────────────────────────────────────

type chaosMode string

const (
	chaosNone    chaosMode = ""
	chaosSlow    chaosMode = "slow"
	chaosError   chaosMode = "error"
	chaosRecover chaosMode = "recover"
)

type chaosState struct {
	mu       sync.RWMutex
	mode     chaosMode
	duration int
	rate     float64
}

var chaos = &chaosState{}

func (c *chaosState) set(mode chaosMode, duration int, rate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode = mode
	c.duration = duration
	c.rate = rate

	// Update Prometheus gauge
	switch mode {
	case chaosSlow:
		chaosActiveGauge.Set(1)
	case chaosError:
		chaosActiveGauge.Set(2)
	default:
		chaosActiveGauge.Set(0)
	}
}

func (c *chaosState) apply() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch c.mode {
	case chaosSlow:
		time.Sleep(time.Duration(c.duration) * time.Second)
	case chaosError:
		if rand.Float64() < c.rate {
			return true
		}
	}
	return false
}

// ── Config ────────────────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── responseWriter wrapper (captures status code for metrics) ─────────────────

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// ── Middleware ────────────────────────────────────────────────────────────────

// metricsMiddleware records request count and duration for every request.
// /metrics itself is excluded to avoid self-referential noise.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(sw, r)

		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(sw.status)

		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
	})
}

// modeMiddleware injects X-Mode header in canary and applies chaos.
func modeMiddleware(appMode string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appMode == "canary" {
			w.Header().Set("X-Mode", "canary")
			if chaos.apply() {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "chaos error injection active",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// jsonMiddleware sets Content-Type on all non-metrics responses.
func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleRoot(appMode, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"message":   fmt.Sprintf("Welcome to SwiftDeploy — running in %s mode", appMode),
			"mode":      appMode,
			"version":   version,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime).Seconds()
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"uptime": fmt.Sprintf("%.2fs", uptime),
	})
}

func handleChaos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Mode     string  `json:"mode"`
		Duration int     `json:"duration"`
		Rate     float64 `json:"rate"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	switch body.Mode {
	case "slow":
		if body.Duration <= 0 {
			http.Error(w, `{"error":"duration must be > 0 for slow mode"}`, http.StatusBadRequest)
			return
		}
		chaos.set(chaosSlow, body.Duration, 0)
		json.NewEncoder(w).Encode(map[string]string{
			"chaos":    "slow",
			"duration": fmt.Sprintf("%ds", body.Duration),
		})

	case "error":
		if body.Rate <= 0 || body.Rate > 1 {
			http.Error(w, `{"error":"rate must be between 0 and 1"}`, http.StatusBadRequest)
			return
		}
		chaos.set(chaosError, 0, body.Rate)
		json.NewEncoder(w).Encode(map[string]string{
			"chaos": "error",
			"rate":  fmt.Sprintf("%.0f%%", body.Rate*100),
		})

	case "recover":
		chaos.set(chaosNone, 0, 0)
		json.NewEncoder(w).Encode(map[string]string{"chaos": "recovered"})

	default:
		http.Error(w, `{"error":"unknown chaos mode — use: slow, error, recover"}`, http.StatusBadRequest)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	appMode := getEnv("MODE", "stable")
	version := getEnv("APP_VERSION", "1.0.0")
	port := getEnv("APP_PORT", "3000")

	// Set mode gauge at startup
	if appMode == "canary" {
		appModeGauge.Set(1)
	} else {
		appModeGauge.Set(0)
	}

	// Update uptime gauge every second in background
	go func() {
		for {
			appUptimeSeconds.Set(time.Since(startTime).Seconds())
			time.Sleep(1 * time.Second)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot(appMode, version))
	mux.HandleFunc("/healthz", handleHealthz)
	mux.Handle("/metrics", promhttp.Handler()) // ← NEW

	if appMode == "canary" {
		mux.HandleFunc("/chaos", handleChaos)
		log.Printf("⚠️  Canary mode — chaos endpoint active at POST /chaos")
	}

	// Middleware chain: metrics → json → mode → mux
	handler := metricsMiddleware(jsonMiddleware(modeMiddleware(appMode, mux)))

	log.Printf("🚀 SwiftDeploy app starting on :%s (mode=%s, version=%s)", port, appMode, version)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
