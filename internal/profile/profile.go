// Package profile runs benchmarks under CPU and memory profiling and returns
// the top functions from pprof.
package profile

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Report contains the text output from pprof -top for CPU and memory profiles,
// plus the paths to the profile files themselves for further inspection.
type Report struct {
	CPUTop  string
	MemTop  string
	CPUPath string
	MemPath string
}

// Capture runs the declared benchmarks under CPU and memory profiling,
// returning the top 15 functions from each profile as text output.
// dir is the directory to run benchmarks in, pattern is the benchmark pattern
// (e.g. "Benchmark.*"), benchtime is passed to `go test -benchtime`, and
// timeout bounds the entire operation.
func Capture(ctx context.Context, dir, pattern, benchtime string, timeout time.Duration) (*Report, error) {
	// Create a temporary directory for the profiling outputs and binary.
	tmpDir, err := os.MkdirTemp("", "autoresearch-profile-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cpuProfilePath := filepath.Join(tmpDir, "cpu.out")
	memProfilePath := filepath.Join(tmpDir, "mem.out")
	binaryPath := filepath.Join(tmpDir, "bench.test")

	// Create a context with the specified timeout.
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Run go test with profiling flags.
	testCmd := exec.CommandContext(ctx, "go", "test",
		"-run", "^$",
		"-bench", pattern,
		"-benchtime", benchtime,
		"-count=1",
		"-cpuprofile", cpuProfilePath,
		"-memprofile", memProfilePath,
		"-o", binaryPath,
		"./...",
	)
	testCmd.Dir = dir
	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr

	if err := testCmd.Run(); err != nil {
		return nil, fmt.Errorf("run benchmarks: %w", err)
	}

	// Extract CPU profile using pprof.
	cpuTop, err := runPprof(ctx, binaryPath, cpuProfilePath, "")
	if err != nil {
		return nil, fmt.Errorf("extract CPU profile: %w", err)
	}

	// Extract memory profile using pprof with alloc_space sample index.
	memTop, err := runPprof(ctx, binaryPath, memProfilePath, "alloc_space")
	if err != nil {
		return nil, fmt.Errorf("extract memory profile: %w", err)
	}

	// Copy the profile files to the actual output location.
	// For now, we'll just use the tmpDir paths.
	// The caller can decide where to put them.
	report := &Report{
		CPUTop:  cpuTop,
		MemTop:  memTop,
		CPUPath: cpuProfilePath,
		MemPath: memProfilePath,
	}

	return report, nil
}

// runPprof runs `go tool pprof -top` and returns the output.
// If sampleIndex is non-empty, it adds -sample_index=<sampleIndex>.
func runPprof(ctx context.Context, binary, profile, sampleIndex string) (string, error) {
	args := []string{"tool", "pprof", "-top", "-nodecount=15"}
	if sampleIndex != "" {
		args = append(args, "-sample_index="+sampleIndex)
	}
	args = append(args, binary, profile)

	cmd := exec.CommandContext(ctx, "go", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pprof failed: %w\n%s", err, output)
	}

	return string(output), nil
}
