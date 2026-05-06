package swiftdeploy.canary

import rego.v1

default allow := false

allow if {
	count(violations) == 0
}

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