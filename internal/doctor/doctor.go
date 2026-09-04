// Package doctor checks whether the current machine can measure benchmarks reliably.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/g4lb/autoresearch-go/internal/gitx"
)

// Severity indicates how bad a finding is.
type Severity int

const (
	SeverityOK            Severity = 0
	SeverityWarn          Severity = 1
	SeverityFail          Severity = 2
	SeverityNotApplicable Severity = -1 // Check doesn't apply to this platform/environment
)

// Finding is one check's result.
type Finding struct {
	Name     string
	Detail   string
	Severity Severity
}

// Check runs all diagnostic checks and returns the findings.
// dir is the repository root or a directory inside it.
func Check(dir string) []Finding {
	var findings []Finding

	// Check go version.
	findings = append(findings, checkGo())

	// Check git.
	findings = append(findings, checkGit())

	// Check if directory is a git repository.
	findings = append(findings, checkGitRepo(dir))

	// Check CPU count and GOMAXPROCS.
	findings = append(findings, checkCPU())

	// Check load average (platform-specific).
	findings = append(findings, checkLoadAverage())

	// Platform-specific checks.
	switch runtime.GOOS {
	case "darwin":
		findings = append(findings, checkDarwin())
	case "linux":
		findings = append(findings, checkLinux())
	}

	// Check disk space.
	findings = append(findings, checkDisk(dir))

	return findings
}

// checkGo verifies go is on PATH and reports its version.
func checkGo() Finding {
	cmd := exec.Command("go", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Finding{
			Name:     "go",
			Detail:   "go not found on PATH",
			Severity: SeverityFail,
		}
	}

	// Output is like "go version go1.21.0 linux/amd64"
	outStr := strings.TrimSpace(string(output))
	if !strings.HasPrefix(outStr, "go version go") {
		return Finding{
			Name:     "go",
			Detail:   fmt.Sprintf("unexpected go version output: %s", outStr),
			Severity: SeverityFail,
		}
	}

	// Extract version number (e.g., "1.21.0")
	parts := strings.Fields(outStr)
	if len(parts) < 3 {
		return Finding{
			Name:     "go",
			Detail:   fmt.Sprintf("cannot parse go version: %s", outStr),
			Severity: SeverityFail,
		}
	}

	versionStr := parts[2][2:] // Remove "go" prefix
	if !isVersionAtLeast(versionStr, "1.21") {
		return Finding{
			Name:     "go",
			Detail:   fmt.Sprintf("go %s is too old, need >= 1.21", versionStr),
			Severity: SeverityFail,
		}
	}

	return Finding{
		Name:     "go",
		Detail:   fmt.Sprintf("go %s", versionStr),
		Severity: SeverityOK,
	}
}

// checkGit verifies git is on PATH.
func checkGit() Finding {
	cmd := exec.Command("git", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Finding{
			Name:     "git",
			Detail:   "git not found on PATH",
			Severity: SeverityFail,
		}
	}

	outStr := strings.TrimSpace(string(output))
	return Finding{
		Name:     "git",
		Detail:   outStr,
		Severity: SeverityOK,
	}
}

// checkGitRepo verifies the working tree is a git repository.
func checkGitRepo(dir string) Finding {
	_, err := gitx.Root(dir)
	if err != nil {
		return Finding{
			Name:     "git repo",
			Detail:   fmt.Sprintf("not a git repository: %v", err),
			Severity: SeverityFail,
		}
	}

	return Finding{
		Name:     "git repo",
		Detail:   "working tree is a git repository",
		Severity: SeverityOK,
	}
}

// checkCPU reports the number of logical CPUs and warns if GOMAXPROCS is not pinned.
func checkCPU() Finding {
	numCPU := runtime.NumCPU()
	detail := fmt.Sprintf("%d logical CPU cores", numCPU)

	// Check if GOMAXPROCS is pinned in environment.
	// If GOMAXPROCS env var is set, it takes precedence at startup.
	gomaxprocs := os.Getenv("GOMAXPROCS")
	if gomaxprocs == "" {
		if numCPU > 1 {
			// Heterogeneous cores might cause variance; warn if GOMAXPROCS not pinned.
			return Finding{
				Name:     "gomaxprocs",
				Detail:   detail + "; GOMAXPROCS not pinned (consider setting it in config for more consistent results on heterogeneous cores)",
				Severity: SeverityWarn,
			}
		}
	} else {
		detail += fmt.Sprintf(" (GOMAXPROCS=%s)", gomaxprocs)
	}

	return Finding{
		Name:     "gomaxprocs",
		Detail:   detail,
		Severity: SeverityOK,
	}
}

// checkLoadAverage checks the 1-minute load average where available.
func checkLoadAverage() Finding {
	// Try to read from /proc/loadavg (Linux only).
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist; on macOS or Windows, load average is not readily available.
			return Finding{
				Name:     "load",
				Detail:   fmt.Sprintf("1-minute load average: not checked (not available on %s)", runtime.GOOS),
				Severity: SeverityNotApplicable,
			}
		}
		// Some other error reading the file.
		return Finding{
			Name:     "load",
			Detail:   fmt.Sprintf("1-minute load average: unknown (error reading /proc/loadavg: %v)", err),
			Severity: SeverityOK,
		}
	}

	parts := strings.Fields(string(content))
	if len(parts) < 1 {
		return Finding{
			Name:     "load",
			Detail:   "1-minute load average: unknown (parse error)",
			Severity: SeverityOK,
		}
	}

	loadStr := parts[0]
	load, err := strconv.ParseFloat(loadStr, 64)
	if err != nil {
		return Finding{
			Name:     "load",
			Detail:   "1-minute load average: unknown (parse error)",
			Severity: SeverityOK,
		}
	}

	numCPU := float64(runtime.NumCPU())
	threshold := 0.5 * numCPU
	severity := SeverityOK
	if load > threshold {
		severity = SeverityWarn
	}

	return Finding{
		Name:     "load",
		Detail:   fmt.Sprintf("1-minute load average: %.2f (threshold: %.2f)", load, threshold),
		Severity: severity,
	}
}

// checkDarwin returns darwin-specific findings.
func checkDarwin() Finding {
	return Finding{
		Name:     "darwin",
		Detail:   "P/E core scheduling adds variance; close other applications for consistent results",
		Severity: SeverityWarn,
	}
}

// checkLinux checks the CPU scaling governor.
func checkLinux() Finding {
	const governorPath = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"

	content, err := os.ReadFile(governorPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing cpufreq is normal on VMs/containers; report unknown.
			return Finding{
				Name:     "cpufreq",
				Detail:   "CPU scaling governor: unknown (cpufreq not available, likely VM or container)",
				Severity: SeverityOK,
			}
		}
		// Some other error reading the file.
		return Finding{
			Name:     "cpufreq",
			Detail:   fmt.Sprintf("CPU scaling governor: unknown (error reading %s: %v)", governorPath, err),
			Severity: SeverityOK,
		}
	}

	governor := strings.TrimSpace(string(content))
	severity := SeverityOK
	if governor != "performance" {
		severity = SeverityWarn
	}

	return Finding{
		Name:     "cpufreq",
		Detail:   fmt.Sprintf("CPU scaling governor: %s", governor),
		Severity: severity,
	}
}

// checkDisk checks available disk space in the given directory.
// Platform-specific implementation in disk_unix.go and disk_other.go.
func checkDisk(dir string) Finding {
	return checkDiskSpace(dir)
}

// isVersionAtLeast checks if version >= minVersion.
// Simple comparison: "1.21.5" >= "1.21" is true, "1.20.9" >= "1.21" is false.
func isVersionAtLeast(version, minVersion string) bool {
	parts := strings.Split(version, ".")
	minParts := strings.Split(minVersion, ".")

	// Compare major and minor versions.
	for i := 0; i < len(minParts) && i < len(parts); i++ {
		v, _ := strconv.Atoi(parts[i])
		m, _ := strconv.Atoi(minParts[i])
		if v > m {
			return true
		}
		if v < m {
			return false
		}
	}

	// If we've matched all minParts, and we have at least as many parts as minParts, it's OK.
	return len(parts) >= len(minParts)
}
