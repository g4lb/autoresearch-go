// Package profile runs benchmarks under CPU and memory profiling and returns
// the top functions from pprof.
package profile

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/g4lb/autor3search-go/internal/discover"
	"github.com/g4lb/autor3search-go/internal/runner"
)

// PackageReport is the CPU/memory profiling output for one package.
type PackageReport struct {
	// Dir is the repo-relative directory of the profiled package, "." at
	// the repository root.
	Dir string
	// CPUTop is the `pprof -top` output for the CPU profile.
	CPUTop string
	// MemTop is the `pprof -top` output for the memory profile
	// (alloc_space sample index).
	MemTop string
	// CPUPath is where the raw CPU profile was written.
	CPUPath string
	// MemPath is where the raw memory profile was written.
	MemPath string
}

// Report contains one PackageReport per profiled package, in the order the
// directories were requested. Most repositories have all of their declared
// benchmarks in a single package, in which case Report has exactly one
// entry; a repository whose benchmarks are spread across packages produces
// one entry per package, since the go tool refuses -cpuprofile/-memprofile
// against a pattern that matches more than one package.
type Report struct {
	// Packages holds one entry per profiled package.
	Packages []PackageReport
}

// Dirs resolves the distinct repo-relative directories that contain the
// benchmarks named in names, by discovering every benchmark in root and
// filtering. An empty names selects every discovered benchmark. The
// returned directories are sorted for a deterministic profiling order.
//
// Dirs fails if none of the named benchmarks (or, when names is empty, no
// benchmark at all) were found anywhere under root.
func Dirs(root string, names []string) ([]string, error) {
	benches, err := discover.Benchmarks(root)
	if err != nil {
		return nil, fmt.Errorf("discover benchmarks: %w", err)
	}

	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	seen := map[string]bool{}
	var dirs []string
	for _, b := range benches {
		if len(names) > 0 && !want[b.Name] {
			continue
		}
		if !seen[b.Dir] {
			seen[b.Dir] = true
			dirs = append(dirs, b.Dir)
		}
	}

	if len(dirs) == 0 {
		if len(names) > 0 {
			return nil, fmt.Errorf("no discovered benchmark matches the configured benchmarks (%s); "+
				"check .autor3search/config.yaml's benchmarks: list against what `go/ast` finds in %s",
				strings.Join(names, ", "), root)
		}
		return nil, fmt.Errorf("no benchmarks discovered in %s", root)
	}

	sort.Strings(dirs)
	return dirs, nil
}

// Capture runs the declared benchmarks under CPU and memory profiling,
// returning the top 15 functions from each profile as text output.
//
// root is the repository root, dirs is the (already-resolved, distinct)
// list of repo-relative package directories to profile — see Dirs — pattern
// is the benchmark pattern (e.g. "Benchmark.*"), benchtime is passed to
// `go test -benchtime`, dest is the directory where profile files are
// written (.autor3search/profiles or similar), and timeout bounds the
// entire operation.
//
// The go tool refuses -cpuprofile/-memprofile against a package pattern
// that matches more than one package, so Capture profiles each directory in
// dirs with its own `go test` invocation rather than a single `go test
// ./...`. When len(dirs) == 1 — the common case — profile files are written
// directly to dest, matching earlier behavior. When there is more than one,
// each package's files are written to their own subdirectory of dest so
// they do not overwrite one another.
func Capture(ctx context.Context, root string, dirs []string, pattern, benchtime, dest string, timeout time.Duration) (*Report, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no package directories to profile")
	}

	// Create a temporary directory for the compiled test binaries only.
	tmpDir, err := os.MkdirTemp("", "autor3search-profile-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Ensure destination directory exists.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("create destination dir: %w", err)
	}

	report := &Report{}
	for i, dir := range dirs {
		pkgDest := dest
		if len(dirs) > 1 {
			pkgDest = pkgDestDir(dest, dir)
			if err := os.MkdirAll(pkgDest, 0o755); err != nil {
				return nil, fmt.Errorf("create destination dir for package %s: %w", dir, err)
			}
		}

		// Profile files go to their final destination, not to the temp dir.
		cpuProfilePath := filepath.Join(pkgDest, "cpu.out")
		memProfilePath := filepath.Join(pkgDest, "mem.out")
		binaryPath := filepath.Join(tmpDir, fmt.Sprintf("bench-%d.test", i))

		// Run go test with profiling flags via the runner, which handles
		// process groups, timeouts, and output buffering in one place.
		r := runner.New(root, timeout, nil)
		result, err := r.BenchProfile(ctx, pkgArg(dir), pattern, benchtime, cpuProfilePath, memProfilePath, binaryPath)
		if err != nil {
			return nil, fmt.Errorf("run benchmarks for package %s: %w", dir, err)
		}
		if !result.OK() {
			// Include stderr if available, otherwise stdout
			errOutput := string(result.Stderr)
			if errOutput == "" {
				errOutput = string(result.Stdout)
			}
			return nil, fmt.Errorf("run benchmarks for package %s: exit code %d, timed out: %v\n%s",
				dir, result.ExitCode, result.TimedOut, errOutput)
		}

		// Extract CPU profile using pprof.
		cpuTop, err := runPprof(ctx, binaryPath, cpuProfilePath, "")
		if err != nil {
			return nil, fmt.Errorf("extract CPU profile for package %s: %w", dir, err)
		}

		// Extract memory profile using pprof with alloc_space sample index.
		memTop, err := runPprof(ctx, binaryPath, memProfilePath, "alloc_space")
		if err != nil {
			return nil, fmt.Errorf("extract memory profile for package %s: %w", dir, err)
		}

		report.Packages = append(report.Packages, PackageReport{
			Dir:     dir,
			CPUTop:  cpuTop,
			MemTop:  memTop,
			CPUPath: cpuProfilePath,
			MemPath: memProfilePath,
		})
	}

	return report, nil
}

// pkgArg turns a discover.Benchmark.Dir value into the `go test` package
// pattern that selects exactly that package: "." for the repository root,
// "./<dir>" otherwise. It deliberately never returns "./..." — the go tool
// refuses -cpuprofile/-memprofile against a pattern matching more than one
// package.
func pkgArg(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// pkgDestDir returns the destination directory for one package's profile
// files when profiling more than one package, nesting them under dest by
// directory so that, e.g., ".autor3search/profiles/english/cpu.out" and
// ".autor3search/profiles/cpu.out" cannot collide.
func pkgDestDir(dest, dir string) string {
	if dir == "." {
		return dest
	}
	return filepath.Join(dest, filepath.FromSlash(dir))
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
