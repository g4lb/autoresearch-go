package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/profile"
)

// runProfile profiles the declared benchmarks and reports hot spots.
func runProfile(args []string) int {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	root, err := gitx.Root(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %s is not inside a git repository: %v\n", *dir, err)
		return exitUsage
	}

	configPath := filepath.Join(root, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		fmt.Fprintln(os.Stderr, "run `autoresearch-go init` first.")
		return exitUsage
	}

	// Create profiles directory.
	profileDir := filepath.Join(root, ".autoresearch", "profiles")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	// Parse timeout.
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	// Run profiling.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	benchPattern := "Benchmark" + benchmarkPattern(cfg.Benchmarks)
	report, err := profile.Capture(ctx, root, benchPattern, cfg.Benchtime, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	// Copy profiles to output directory and update paths.
	cpuOutPath := filepath.Join(profileDir, "cpu.out")
	memOutPath := filepath.Join(profileDir, "mem.out")

	if err := copyProfileFile(report.CPUPath, cpuOutPath); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: copy CPU profile: %v\n", err)
		return exitUsage
	}
	if err := copyProfileFile(report.MemPath, memOutPath); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: copy memory profile: %v\n", err)
		return exitUsage
	}

	// Print the output.
	fmt.Println("CPU (top 15) ------------------------------------------")
	fmt.Println(report.CPUTop)
	fmt.Println()
	fmt.Println("Allocations (top 15) ----------------------------------")
	fmt.Println(report.MemTop)
	fmt.Println()
	fmt.Printf("profiles written to .autoresearch/profiles/{cpu,mem}.out\n")
	fmt.Printf("open with: go tool pprof -http=: .autoresearch/profiles/cpu.out\n")

	return exitOK
}

// benchmarkPattern constructs a pattern for go test -bench from benchmark names.
// If names are ["BenchmarkCountWords"], returns ".*", else returns pattern matching all.
func benchmarkPattern(names []string) string {
	if len(names) == 0 {
		return ".*"
	}
	return ".*"
}

// copyProfileFile copies src to dst.
func copyProfileFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
