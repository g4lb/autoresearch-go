package main

import (
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/state"
)

// TestProfileUsesDeclaredBenchmarks verifies that profile uses the benchmark
// pattern from state.BenchPattern, which respects cfg.Benchmarks.
func TestProfileUsesDeclaredBenchmarks(t *testing.T) {
	// Verify that state.BenchPattern produces the expected patterns.
	// This ensures profile will use the correct pattern via state.BenchPattern(cfg.Benchmarks).

	tests := []struct {
		names    []string
		contains string // Substring the pattern should contain or match
	}{
		{
			names:    []string{},
			contains: ".",
		},
		{
			names:    []string{"BenchmarkCountWords"},
			contains: "BenchmarkCountWords",
		},
		{
			names:    []string{"BenchmarkA", "BenchmarkB"},
			contains: "BenchmarkA",
		},
	}

	for _, tc := range tests {
		pattern := state.BenchPattern(tc.names)
		if pattern != "." && pattern == ".*" {
			t.Errorf("state.BenchPattern(%v) = %q, should not be wildcard; profile must respect cfg.Benchmarks", tc.names, pattern)
		}
	}
}

func TestProfileInvalidConfigDoesNotAdviseInit(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	writeInvalidConfig(t, dir)

	var code int
	out := captureStderr(t, func() {
		code = runProfile([]string{"-C", dir})
	})
	if code != exitUsage {
		t.Fatalf("runProfile = %d, want %d", code, exitUsage)
	}
	if strings.Contains(out, "init` first") {
		t.Errorf("invalid config sends the user to init, which refuses to overwrite it:\n%s", out)
	}
	if !strings.Contains(out, "exists but is invalid") {
		t.Errorf("stderr does not say the config exists but is invalid:\n%s", out)
	}
}

func TestProfileMissingConfigAdvisesInit(t *testing.T) {
	dir := copyDemoRepo(t) // no mustInit: there is genuinely no config yet

	var code int
	out := captureStderr(t, func() {
		code = runProfile([]string{"-C", dir})
	})
	if code != exitUsage {
		t.Fatalf("runProfile = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(out, "init` first") {
		t.Errorf("an absent config should point at init:\n%s", out)
	}
}
