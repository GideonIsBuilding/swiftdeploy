package internal

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HostStats holds the real-time resource readings sent to OPA pre-deploy.
type HostStats struct {
	DiskFreeGB        float64 `json:"disk_free_gb"`
	CPULoad           float64 `json:"cpu_load"`
	MemoryUsedPercent float64 `json:"memory_used_percent"`
}

// CollectHostStats gathers live host metrics from the OS.
func CollectHostStats() (*HostStats, error) {
	disk, err := diskFreeGB()
	if err != nil {
		return nil, fmt.Errorf("collecting disk stats: %w", err)
	}
	cpu, err := cpuLoad()
	if err != nil {
		return nil, fmt.Errorf("collecting CPU stats: %w", err)
	}
	mem, err := memUsedPercent()
	if err != nil {
		return nil, fmt.Errorf("collecting memory stats: %w", err)
	}
	return &HostStats{
		DiskFreeGB:        disk,
		CPULoad:           cpu,
		MemoryUsedPercent: mem,
	}, nil
}

// diskFreeGB returns free disk space in GB for the current directory.
func diskFreeGB() (float64, error) {
	var out []byte
	var err error

	if runtime.GOOS == "darwin" {
		out, err = exec.Command("df", "-k", ".").Output()
	} else {
		out, err = exec.Command("df", "-k", ".").Output()
	}
	if err != nil {
		return 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output")
	}

	// df -k output: Filesystem 1K-blocks Used Available Use% Mounted
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0, fmt.Errorf("unexpected df fields: %v", fields)
	}

	// Available is column index 3
	avail, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing available blocks: %w", err)
	}

	// Convert KB → GB
	return avail / (1024 * 1024), nil
}

// cpuLoad returns the 1-minute load average.
func cpuLoad() (float64, error) {
	var out []byte
	var err error

	if runtime.GOOS == "darwin" {
		// macOS: sysctl -n vm.loadavg → { 0.52 0.48 0.45 }
		out, err = exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return 0, err
		}
		// format: { 0.52 0.48 0.45 }
		s := strings.Trim(strings.TrimSpace(string(out)), "{} \n")
		parts := strings.Fields(s)
		if len(parts) < 1 {
			return 0, fmt.Errorf("unexpected sysctl output: %s", out)
		}
		return strconv.ParseFloat(parts[0], 64)
	}

	// Linux: /proc/loadavg → "0.52 0.48 0.45 1/432 12345"
	out, err = exec.Command("cat", "/proc/loadavg").Output()
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(out))
	if len(parts) < 1 {
		return 0, fmt.Errorf("unexpected /proc/loadavg output")
	}
	return strconv.ParseFloat(parts[0], 64)
}

// memUsedPercent returns percentage of RAM currently in use.
func memUsedPercent() (float64, error) {
	if runtime.GOOS == "darwin" {
		// Use vm_stat on macOS
		out, err := exec.Command("vm_stat").Output()
		if err != nil {
			return 0, err
		}

		var pagesFree, pagesActive, pagesInactive, pagesWired, pagesCompressed float64
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) != 2 {
				continue
			}
			key := strings.TrimSpace(fields[0])
			val, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(fields[1]), "."), 64)
			if err != nil {
				continue
			}
			switch key {
			case "Pages free":
				pagesFree = val
			case "Pages active":
				pagesActive = val
			case "Pages inactive":
				pagesInactive = val
			case "Pages wired down":
				pagesWired = val
			case "Pages occupied by compressor":
				pagesCompressed = val
			}
		}

		total := pagesFree + pagesActive + pagesInactive + pagesWired + pagesCompressed
		if total == 0 {
			return 0, fmt.Errorf("could not calculate total memory pages")
		}
		used := pagesActive + pagesWired + pagesCompressed
		return (used / total) * 100, nil
	}

	// Linux: parse /proc/meminfo
	out, err := exec.Command("cat", "/proc/meminfo").Output()
	if err != nil {
		return 0, err
	}

	var memTotal, memAvailable float64
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "MemAvailable:":
			memAvailable = val
		}
	}

	if memTotal == 0 {
		return 0, fmt.Errorf("could not read MemTotal from /proc/meminfo")
	}
	return ((memTotal - memAvailable) / memTotal) * 100, nil
}
