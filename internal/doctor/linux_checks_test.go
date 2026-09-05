package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDefaultLinuxPaths_Governor pins the real governor path used in
// production (defaultLinuxPaths.governor, wired up by checkLinux()).
//
// This is deliberately a literal, environment-independent assertion: on a
// machine or CI runner that happens not to expose cpufreq at all (common in
// containers, and observed on Docker-for-Mac's Linux VM), a wrong path and
// the correct path are indistinguishable at runtime — both simply report
// "not checked" via os.IsNotExist. TestCheckLinux_RealFilesystem_Linux
// therefore cannot be relied on alone to catch a wrong-path regression in
// every environment. This test catches it everywhere, on every platform,
// by pinning the exact string.
func TestDefaultLinuxPaths_Governor(t *testing.T) {
	const want = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	if defaultLinuxPaths.governor != want {
		t.Errorf("defaultLinuxPaths.governor = %q, want %q", defaultLinuxPaths.governor, want)
	}
	// Guard against the specific historical bug: the path must live under
	// /sys, never under the nonexistent /proc/sys/devices/... .
	if strings.HasPrefix(defaultLinuxPaths.governor, "/proc/sys") {
		t.Errorf("defaultLinuxPaths.governor = %q, must not be rooted at /proc/sys (that path never exists)", defaultLinuxPaths.governor)
	}
}

// TestCheckLinuxAt_Governor exercises the CPU scaling governor check against
// synthetic files instead of the real /sys filesystem, so it runs (and can
// fail) on every platform, not just Linux.
//
// This is the test that would have caught the historical bug where the
// governor path was specified as "/proc/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
// (nonexistent) instead of "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor":
// reading a nonexistent path is os.IsNotExist, exactly like a real machine
// without cpufreq, so the wrong path silently reported "not checked" on
// every Linux box instead of failing.
func TestCheckLinuxAt_Governor(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(t *testing.T, name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return path
	}

	t.Run("performance governor passes", func(t *testing.T) {
		path := writeFile(t, "governor-performance", "performance")
		f := checkLinuxAt(path)
		if f.Severity != SeverityOK {
			t.Errorf("severity = %v, want SeverityOK", f.Severity)
		}
		if !strings.Contains(f.Detail, "performance") {
			t.Errorf("detail = %q, want it to mention performance", f.Detail)
		}
	})

	t.Run("performance governor with trailing newline passes", func(t *testing.T) {
		// Real /sys files (like real /proc files) end in a trailing newline.
		path := writeFile(t, "governor-performance-nl", "performance\n")
		f := checkLinuxAt(path)
		if f.Severity != SeverityOK {
			t.Errorf("severity = %v, want SeverityOK", f.Severity)
		}
		if strings.Contains(f.Detail, "\n") || strings.Contains(f.Detail, " performance ") {
			t.Errorf("detail = %q, whitespace was not trimmed", f.Detail)
		}
		if !strings.HasSuffix(f.Detail, "performance") {
			t.Errorf("detail = %q, want it to end with the trimmed governor value", f.Detail)
		}
	})

	nonPerformanceCases := []string{"powersave", "ondemand", "schedutil", "conservative"}
	for _, governor := range nonPerformanceCases {
		governor := governor
		t.Run("non-performance governor "+governor+" warns", func(t *testing.T) {
			path := writeFile(t, "governor-"+governor, governor+"\n")
			f := checkLinuxAt(path)
			if f.Severity != SeverityWarn {
				t.Errorf("severity = %v, want SeverityWarn", f.Severity)
			}
			if !strings.Contains(f.Detail, governor) {
				t.Errorf("detail = %q, want it to contain the actual value %q", f.Detail, governor)
			}
		})
	}

	t.Run("missing file is not checked, not a pass, not a warn", func(t *testing.T) {
		path := filepath.Join(dir, "does-not-exist")
		f := checkLinuxAt(path)
		if f.Severity != SeverityNotApplicable {
			t.Errorf("severity = %v, want SeverityNotApplicable", f.Severity)
		}
		if f.Severity == SeverityOK {
			t.Error("missing governor file must not report as a pass")
		}
		if f.Severity == SeverityWarn {
			t.Error("missing governor file must not report as a warn")
		}
	})

	t.Run("unreadable file is distinguished from missing, and is not checked", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission bits behave differently on windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits do not block root reads")
		}
		path := writeFile(t, "governor-unreadable", "performance")
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		defer os.Chmod(path, 0o644) // let TempDir cleanup remove it

		f := checkLinuxAt(path)
		if f.Severity != SeverityNotApplicable {
			t.Errorf("severity = %v, want SeverityNotApplicable", f.Severity)
		}
		if !strings.Contains(f.Detail, "permission denied") {
			t.Errorf("detail = %q, want it to distinguish permission-denied from missing", f.Detail)
		}
	})
}

// TestCheckLoadAverageAt exercises the load-average check against synthetic
// files instead of the real /proc/loadavg, so parsing and threshold logic
// run (and can fail) on every platform.
func TestCheckLoadAverageAt(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(t *testing.T, name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return path
	}

	t.Run("well-formed low load passes", func(t *testing.T) {
		// Real /proc/loadavg looks like: "0.10 0.20 0.15 1/234 5678"
		path := writeFile(t, "loadavg-low", "0.01 0.05 0.10 1/234 5678\n")
		f := checkLoadAverageAt(path)
		if f.Severity != SeverityOK {
			t.Errorf("severity = %v, want SeverityOK, detail=%q", f.Severity, f.Detail)
		}
		if !strings.Contains(f.Detail, "0.01") {
			t.Errorf("detail = %q, want it to contain the parsed 1-minute value", f.Detail)
		}
	})

	t.Run("well-formed high load warns", func(t *testing.T) {
		// Threshold is 0.5 * NumCPU; a very large load average exceeds it
		// regardless of how many CPUs the test machine has.
		path := writeFile(t, "loadavg-high", "999.00 500.00 250.00 3/456 7890\n")
		f := checkLoadAverageAt(path)
		if f.Severity != SeverityWarn {
			t.Errorf("severity = %v, want SeverityWarn, detail=%q", f.Severity, f.Detail)
		}
	})

	malformedCases := map[string]string{
		"empty file":          "",
		"non-numeric field":   "not-a-number 0.20 0.15 1/234 5678\n",
		"only whitespace":     "   \n",
		"garbage single word": "garbage\n",
	}
	for name, content := range malformedCases {
		name, content := name, content
		t.Run("malformed content ("+name+") is not checked", func(t *testing.T) {
			path := writeFile(t, "loadavg-malformed-"+strings.ReplaceAll(name, " ", "-"), content)
			f := checkLoadAverageAt(path)
			if f.Severity != SeverityNotApplicable {
				t.Errorf("severity = %v, want SeverityNotApplicable, detail=%q", f.Severity, f.Detail)
			}
		})
	}

	t.Run("absent source is not checked, not a pass, not a warn", func(t *testing.T) {
		path := filepath.Join(dir, "does-not-exist")
		f := checkLoadAverageAt(path)
		if f.Severity != SeverityNotApplicable {
			t.Errorf("severity = %v, want SeverityNotApplicable", f.Severity)
		}
	})
}

// TestCheckLinux_RealFilesystem_Linux runs the REAL governor check against
// the REAL filesystem when running on Linux (e.g. the ubuntu-latest CI
// runner). It accepts either a genuine governor reading or the "not
// checked" state (GitHub's runners may not expose cpufreq), but never a
// crash, an empty detail, or a malformed severity. This is the test that
// would have caught the historical "/proc/sys/devices/..." wrong-path bug
// on the very first CI run on Linux.
func TestCheckLinux_RealFilesystem_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only real-filesystem check; skipping on " + runtime.GOOS)
	}

	f := checkLinux()

	if f.Name != "cpufreq" {
		t.Errorf("Name = %q, want %q", f.Name, "cpufreq")
	}
	if strings.TrimSpace(f.Detail) == "" {
		t.Error("Detail is empty")
	}

	switch f.Severity {
	case SeverityOK, SeverityWarn:
		// A genuine reading: OK means governor == "performance", Warn means
		// it isn't. Either way the detail must carry the actual value, not
		// a placeholder like "unknown".
		if !strings.HasPrefix(f.Detail, "CPU scaling governor: ") {
			t.Fatalf("unexpected detail shape for a real reading: %q", f.Detail)
		}
		value := strings.TrimPrefix(f.Detail, "CPU scaling governor: ")
		if value == "" || strings.Contains(value, "unknown") || strings.Contains(value, "not checked") {
			t.Errorf("real reading looks malformed/garbage: %q", f.Detail)
		}
	case SeverityNotApplicable:
		// cpufreq genuinely unavailable on this runner (containerized
		// runner, missing kernel module, etc). Acceptable, but must be
		// reported as "not checked", not silently as OK.
		if !strings.Contains(f.Detail, "not checked") {
			t.Errorf("NotApplicable finding should say 'not checked': %q", f.Detail)
		}
	default:
		t.Fatalf("unexpected severity %v (detail=%q) — real Linux check must never fail or return a garbage severity", f.Severity, f.Detail)
	}
}

// TestCheckLoadAverage_RealFilesystem_Linux runs the REAL load-average check
// against the REAL /proc/loadavg when running on Linux, verifying the
// well-known-to-exist file parses into a sane, non-negative value.
func TestCheckLoadAverage_RealFilesystem_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only real-filesystem check; skipping on " + runtime.GOOS)
	}

	f := checkLoadAverage()

	if f.Name != "load" {
		t.Errorf("Name = %q, want %q", f.Name, "load")
	}
	// /proc/loadavg always exists on Linux, so this must be a genuine
	// reading (OK or Warn), never "not checked".
	if f.Severity != SeverityOK && f.Severity != SeverityWarn {
		t.Fatalf("severity = %v, want a genuine reading (OK or Warn) since /proc/loadavg always exists on Linux; detail=%q", f.Severity, f.Detail)
	}
	if strings.Contains(f.Detail, "unknown") || strings.Contains(f.Detail, "not checked") {
		t.Errorf("real reading looks malformed/garbage: %q", f.Detail)
	}
}
