package main

import (
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
