// Package profile runs benchmarks under CPU and memory profiling and returns
// the top functions from pprof.
package profile

import (
	"bytes"
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
// (e.g. "Benchmark.*"), benchtime is passed to `go test -benchtime`, dest is
// the directory where profile files are written (.autoresearch/profiles or similar),
// and timeout bounds the entire operation.
func Capture(ctx context.Context, dir, pattern, benchtime, dest string, timeout time.Duration) (*Report, error) {
	// Create a temporary directory for the compiled test binary only.
	tmpDir, err := os.MkdirTemp("", "autoresearch-profile-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Ensure destination directory exists.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("create destination dir: %w", err)
	}

	// Profile files go to their final destination, not to the temp dir.
	cpuProfilePath := filepath.Join(dest, "cpu.out")
	memProfilePath := filepath.Join(dest, "mem.out")
	binaryPath := filepath.Join(tmpDir, "bench.test")

	// Create a context with the specified timeout.
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Run go test with profiling flags, capturing output to avoid flooding context.
	var stdout, stderr bytes.Buffer
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
	testCmd.Stdout = &stdout
	testCmd.Stderr = &stderr

	if err := testCmd.Run(); err != nil {
		return nil, fmt.Errorf("run benchmarks: %w\n%s", err, stderr.String())
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
