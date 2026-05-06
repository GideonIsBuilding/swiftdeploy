# canary.rego
# Domain: canary service health before promotion.
# Owns exactly one question: "Is the canary safe to promote?"
# Thresholds come from data.json — never hardcoded here.

package swiftdeploy.canary

import rego.v1

# ── Default: deny unless explicitly allowed ───────────────────────────────────

default allow := false

allow if {
	count(violations) == 0
}

# ── Violations ────────────────────────────────────────────────────────────────

violations contains msg if {
	input.error_rate_percent > data.canary.max_error_rate_percent
	msg := sprintf(
		"error_rate %.4f%% exceeds maximum %.2f%% — canary is too unstable to promote",
		[input.error_rate_percent, data.canary.max_error_rate_percent],
	)
}

violations contains msg if {
	input.p99_latency_ms > data.canary.max_p99_latency_ms
	msg := sprintf(
		"p99_latency %.2fms exceeds maximum %.2fms — canary is too slow to promote",
		[input.p99_latency_ms, data.canary.max_p99_latency_ms],
	)
}

# ── Decision (always carry reasoning) ────────────────────────────────────────

decision := {
	"allow":      allow,
	"violations": violations,
	"contact":    data.contact,
	"checked_at": input.checked_at,
	"input_snapshot": {
		"error_rate_percent": input.error_rate_percent,
		"p99_latency_ms":     input.p99_latency_ms,
		"sample_window_secs": input.sample_window_secs,
	},
}
