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
	"github.com/g4lb/autoresearch-go/internal/state"
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

	// Parse timeout.
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	// Profile destination directory.
	profileDir := filepath.Join(root, ".autoresearch", "profiles")

	// Run profiling. Capture writes directly to profileDir.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Resolve which package(s) actually declare the benchmarks in scope:
	// `go test -cpuprofile` refuses a pattern matching more than one
	// package, so ./... is not an option here (unlike eval's plain
	// `go test ./... -bench`, which has no such restriction).
	dirs, err := profile.Dirs(root, cfg.Benchmarks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	benchPattern := state.BenchPattern(cfg.Benchmarks)
	report, err := profile.Capture(ctx, root, dirs, benchPattern, cfg.Benchtime, profileDir, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go profile: %v\n", err)
		return exitUsage
	}

	// Print the output, grouped by package: an unlabelled merge of two
	// packages' hot spots would be actively misleading.
	multi := len(report.Packages) > 1
	for _, pkg := range report.Packages {
		if multi {
			fmt.Printf("=== package %s ===\n", pkg.Dir)
		}
		fmt.Println("CPU (top 15) ------------------------------------------")
		fmt.Println(pkg.CPUTop)
		fmt.Println()
		fmt.Println("Allocations (top 15) ----------------------------------")
		fmt.Println(pkg.MemTop)
		fmt.Println()
	}

	if multi {
		fmt.Println("profiles written under .autoresearch/profiles/<package>/{cpu,mem}.out:")
		for _, pkg := range report.Packages {
			fmt.Printf("  %s: %s, %s\n", pkg.Dir, pkg.CPUPath, pkg.MemPath)
		}
		fmt.Printf("open with: go tool pprof -http=: <path above>\n")
	} else {
		fmt.Printf("profiles written to .autoresearch/profiles/{cpu,mem}.out\n")
		fmt.Printf("open with: go tool pprof -http=: .autoresearch/profiles/cpu.out\n")
	}

	return exitOK
}
