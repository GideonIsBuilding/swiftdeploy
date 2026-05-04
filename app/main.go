package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

// ── Process start time (for uptime) ─────────────────────────────────────────

var startTime = time.Now()

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
	duration int     // seconds, for "slow"
	rate     float64 // 0-1, for "error"
}

var chaos = &chaosState{}

func (c *chaosState) set(mode chaosMode, duration int, rate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode = mode
	c.duration = duration
	c.rate = rate
}

func (c *chaosState) apply() (shouldError bool) {
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

// ── Middleware ────────────────────────────────────────────────────────────────

// modeMiddleware injects X-Mode: canary on every response when in canary mode,
// and applies any active chaos effects.
func modeMiddleware(appMode string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appMode == "canary" {
			w.Header().Set("X-Mode", "canary")

			// Apply chaos (error injection happens before writing body)
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

// jsonMiddleware sets Content-Type on all responses
func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
			"chaos": "slow",
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
		json.NewEncoder(w).Encode(map[string]string{
			"chaos": "recovered",
		})

	default:
		http.Error(w, `{"error":"unknown chaos mode — use: slow, error, recover"}`, http.StatusBadRequest)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	appMode := getEnv("MODE", "stable")
	version := getEnv("APP_VERSION", "1.0.0")
	port := getEnv("APP_PORT", "3000")

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot(appMode, version))
	mux.HandleFunc("/healthz", handleHealthz)

	if appMode == "canary" {
		mux.HandleFunc("/chaos", handleChaos)
		log.Printf("⚠️  Canary mode — chaos endpoint active at POST /chaos")
	}

	handler := jsonMiddleware(modeMiddleware(appMode, mux))

	log.Printf("🚀 SwiftDeploy app starting on :%s (mode=%s, version=%s)", port, appMode, version)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
