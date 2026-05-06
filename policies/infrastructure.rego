package swiftdeploy.infrastructure

import rego.v1

default allow := false

allow if {
	count(violations) == 0
}

violations contains msg if {
	input.disk_free_gb < data.infrastructure.min_disk_free_gb
	msg := sprintf(
		"disk_free_gb %.2f is below minimum %.2f GB — free up disk space before deploying",
		[input.disk_free_gb, data.infrastructure.min_disk_free_gb],
	)
}

violations contains msg if {
	input.cpu_load > data.infrastructure.max_cpu_load
	msg := sprintf(
		"cpu_load %.2f exceeds maximum %.2f — wait for load to drop before deploying",
		[input.cpu_load, data.infrastructure.max_cpu_load],
	)
}

violations contains msg if {
	input.memory_used_percent > data.infrastructure.max_memory_used_percent
	msg := sprintf(
		"memory_used_percent %.2f%% exceeds maximum %.2f%% — free up memory before deploying",
		[input.memory_used_percent, data.infrastructure.max_memory_used_percent],
	)
}

decision := {
	"allow":      allow,
	"violations": violations,
	"contact":    data.contact,
	"checked_at": input.checked_at,
}
