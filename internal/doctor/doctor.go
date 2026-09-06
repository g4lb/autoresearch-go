// Package doctor checks whether the current machine can measure benchmarks reliably.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/g4lb/autor3search-go/internal/gitx"
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

// linuxPaths holds the filesystem sources that the Linux-specific checks
// read from. Tests substitute alternate paths here so the parsing and
// severity logic can be exercised on any platform, without touching the
// real /sys or /proc filesystem. Production code always uses
// defaultLinuxPaths.
type linuxPaths struct {
	// governor is the CPU scaling governor file for cpu0.
	governor string
	// loadAvg is the kernel's load-average pseudo-file.
	loadAvg string
}

// defaultLinuxPaths are the real paths used outside of tests.
//
// governor was once (wrongly) "/proc/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor",
// which does not exist on any Linux system. Reading a nonexistent path is
// indistinguishable from "cpufreq not available" (both are os.IsNotExist),
// so that bug silently reported "unknown" on every machine instead of
// failing loudly. The correct path lives under /sys, not /proc/sys.
var defaultLinuxPaths = linuxPaths{
	governor: "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor",
	loadAvg:  "/proc/loadavg",
}

// checkLoadAverage checks the 1-minute load average where available.
func checkLoadAverage() Finding {
	return checkLoadAverageAt(defaultLinuxPaths.loadAvg)
}

// checkLoadAverageAt is checkLoadAverage with an injectable source, so it
// can be tested on every platform against synthetic files instead of the
// real /proc/loadavg.
func checkLoadAverageAt(path string) Finding {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist; on macOS or Windows, load average is not readily available.
			return Finding{
				Name:     "load",
				Detail:   fmt.Sprintf("1-minute load average: not checked (not available on %s)", runtime.GOOS),
				Severity: SeverityNotApplicable,
			}
		}
		// Some other error reading the file (e.g. permission denied): the
		// check did not actually run, so it must not count as a pass.
		return Finding{
			Name:     "load",
			Detail:   fmt.Sprintf("1-minute load average: not checked (error reading %s: %v)", path, err),
			Severity: SeverityNotApplicable,
		}
	}

	parts := strings.Fields(string(content))
	if len(parts) < 1 {
		return Finding{
			Name:     "load",
			Detail:   "1-minute load average: not checked (parse error: empty content)",
			Severity: SeverityNotApplicable,
		}
	}

	loadStr := parts[0]
	load, err := strconv.ParseFloat(loadStr, 64)
	if err != nil {
		return Finding{
			Name:     "load",
			Detail:   fmt.Sprintf("1-minute load average: not checked (parse error: %q is not a number)", loadStr),
			Severity: SeverityNotApplicable,
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
	return checkLinuxAt(defaultLinuxPaths.governor)
}

// checkLinuxAt is checkLinux with an injectable governor path, so the
// missing/unreadable/parsing logic can be exercised on every platform
// against synthetic files instead of the real /sys filesystem.
func checkLinuxAt(governorPath string) Finding {
	content, err := os.ReadFile(governorPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing cpufreq is normal on VMs/containers without frequency
			// scaling; the check simply did not run, which is not a pass.
			return Finding{
				Name:     "cpufreq",
				Detail:   "CPU scaling governor: not checked (cpufreq not available, likely VM or container)",
				Severity: SeverityNotApplicable,
			}
		}
		if os.IsPermission(err) {
			// The file exists but we couldn't read it. Distinct from
			// "missing" so a permissions problem doesn't masquerade as a
			// normal VM/container environment.
			return Finding{
				Name:     "cpufreq",
				Detail:   fmt.Sprintf("CPU scaling governor: not checked (permission denied reading %s)", governorPath),
				Severity: SeverityNotApplicable,
			}
		}
		// Some other error reading the file: the check did not run, so it
		// must not count as a pass.
		return Finding{
			Name:     "cpufreq",
			Detail:   fmt.Sprintf("CPU scaling governor: not checked (error reading %s: %v)", governorPath, err),
			Severity: SeverityNotApplicable,
		}
	}

	// /sys files (like /proc files) commonly end in a trailing newline.
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
