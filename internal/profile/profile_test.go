package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/g4lb/autor3search-go/internal/runner"
)

// TestCaptureSinglePackage covers the common case: every declared benchmark
// lives in one package, so Capture must still write its profile files
// directly to dest (not into a per-package subdirectory), matching the
// behavior before multi-package support existed.
func TestCaptureSinglePackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// 300ms of benchmark time gives the 100 Hz CPU profiler ~30 samples.
	// That is the smallest budget at which the CountWords assertion below
	// is about the fixture rather than about scheduling luck — see
	// minCPUSamples. The multi-package test can stay at 20ms because it
	// only asserts the tables are non-empty.
	destDir := t.TempDir()
	ctx := context.Background()

	dirs, err := Dirs("../../testdata/demo", nil)
	if err != nil {
		t.Fatalf("Dirs failed: %v", err)
	}
	if want := []string{"."}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("Dirs = %v, want %v", dirs, want)
	}

	report, err := Capture(ctx, "../../testdata/demo", dirs, "Benchmark.*", "300ms", destDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if len(report.Packages) != 1 {
		t.Fatalf("len(Packages) = %d, want 1: %+v", len(report.Packages), report.Packages)
	}
	pkg := report.Packages[0]

	if pkg.Dir != "." {
		t.Errorf("Dir = %q, want %q", pkg.Dir, ".")
	}

	// Verify that we got non-empty top output mentioning CountWords.
	if pkg.CPUTop == "" {
		t.Error("CPUTop is empty")
	}
	assertEnoughCPUSamples(t, pkg.CPUTop)
	assertProfileBlamesCountWords(t, pkg.CPUPath)

	// Verify that we got non-empty allocations output.
	if pkg.MemTop == "" {
		t.Error("MemTop is empty")
	}

	// The single-package case must write straight to destDir, not a nested
	// per-package directory.
	wantCPU := filepath.Join(destDir, "cpu.out")
	wantMem := filepath.Join(destDir, "mem.out")
	if pkg.CPUPath != wantCPU {
		t.Errorf("CPUPath = %s, want %s", pkg.CPUPath, wantCPU)
	}
	if pkg.MemPath != wantMem {
		t.Errorf("MemPath = %s, want %s", pkg.MemPath, wantMem)
	}

	// CRITICAL: Verify that the profile files actually exist and are non-empty.
	// This test catches the bug where Capture deleted files before returning.
	cpuInfo, err := os.Stat(pkg.CPUPath)
	if err != nil {
		t.Errorf("CPU profile file does not exist: %v", err)
	} else if cpuInfo.Size() == 0 {
		t.Error("CPU profile file is empty")
	}

	memInfo, err := os.Stat(pkg.MemPath)
	if err != nil {
		t.Errorf("memory profile file does not exist: %v", err)
	} else if memInfo.Size() == 0 {
		t.Error("memory profile file is empty")
	}
}

// writeMultiPackageFixture builds, inside t.TempDir(), a two-package module
// where each package declares its own benchmark. testdata/demo must stay
// single-package (Tasks 15 and 17 depend on its exact shape), so this
// fixture is built fresh per test rather than checked in.
func writeMultiPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "go.mod"), "module multipkg\n\ngo 1.21\n")

	mustWriteFile(t, filepath.Join(root, "pkga", "a.go"), `package pkga

// Sum adds a slice of ints together.
func Sum(vals []int) int {
	total := 0
	for _, v := range vals {
		total += v
	}
	return total
}
`)
	mustWriteFile(t, filepath.Join(root, "pkga", "a_test.go"), `package pkga

import "testing"

func BenchmarkSum(b *testing.B) {
	vals := []int{1, 2, 3, 4, 5, 6, 7, 8}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Sum(vals)
	}
}
`)

	mustWriteFile(t, filepath.Join(root, "pkgb", "b.go"), `package pkgb

import "strings"

// Join concatenates strs with a comma.
func Join(strs []string) string {
	return strings.Join(strs, ",")
}
`)
	mustWriteFile(t, filepath.Join(root, "pkgb", "b_test.go"), `package pkgb

import "testing"

func BenchmarkJoin(b *testing.B) {
	strs := []string{"a", "b", "c", "d", "e"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Join(strs)
	}
}
`)

	return root
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCaptureMultiPackage reproduces the real-world bug: a repository whose
// declared benchmarks span more than one package must still be profiled
// successfully, with reports clearly labelled per package, rather than
// failing the way `go test ./... -cpuprofile` does.
func TestCaptureMultiPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	root := writeMultiPackageFixture(t)
	ctx := context.Background()

	dirs, err := Dirs(root, nil)
	if err != nil {
		t.Fatalf("Dirs failed: %v", err)
	}
	if want := []string{"pkga", "pkgb"}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("Dirs = %v, want %v", dirs, want)
	}

	// Prove the fixture actually bites go test's own restriction, so a
	// regression back to `go test ./...` would be caught here rather than
	// only on a real multi-package repository.
	badTmp := t.TempDir()
	r := runner.New(root, 30*time.Second, nil)
	badResult, err := r.BenchProfile(ctx, "./...", "Benchmark.*", "20ms",
		filepath.Join(badTmp, "cpu.out"), filepath.Join(badTmp, "mem.out"), filepath.Join(badTmp, "bad.test"))
	if err != nil {
		t.Fatalf("BenchProfile(./...) transport error: %v", err)
	}
	if badResult.OK() {
		t.Fatal("BenchProfile(./...) unexpectedly succeeded against a multi-package fixture; " +
			"the fixture no longer proves the bug this test guards against")
	}
	const wantErr = "cannot use -cpuprofile flag with multiple packages"
	if !strings.Contains(badResult.Tail(20), wantErr) {
		t.Fatalf("BenchProfile(./...) failed for the wrong reason, want %q:\n%s", wantErr, badResult.Tail(20))
	}

	// The real fix: profile each package directory in turn.
	destDir := t.TempDir()
	report, err := Capture(ctx, root, dirs, "Benchmark.*", "20ms", destDir, 60*time.Second)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if len(report.Packages) != 2 {
		t.Fatalf("len(Packages) = %d, want 2: %+v", len(report.Packages), report.Packages)
	}

	byDir := make(map[string]PackageReport, len(report.Packages))
	for _, pkg := range report.Packages {
		byDir[pkg.Dir] = pkg
	}

	pkgA, ok := byDir["pkga"]
	if !ok {
		t.Fatalf("no report for package pkga: %+v", report.Packages)
	}
	pkgB, ok := byDir["pkgb"]
	if !ok {
		t.Fatalf("no report for package pkgb: %+v", report.Packages)
	}

	for _, pkg := range []struct {
		name   string
		report PackageReport
	}{{"pkga", pkgA}, {"pkgb", pkgB}} {
		if pkg.report.CPUTop == "" {
			t.Errorf("%s: CPUTop is empty", pkg.name)
		}
		if pkg.report.MemTop == "" {
			t.Errorf("%s: MemTop is empty", pkg.name)
		}

		// Each package's profile files must live in their own,
		// clearly-labelled subdirectory so they cannot overwrite each
		// other's data.
		wantDir := filepath.Join(destDir, pkg.name)
		if got := filepath.Dir(pkg.report.CPUPath); got != wantDir {
			t.Errorf("%s: CPUPath dir = %s, want %s", pkg.name, got, wantDir)
		}
		if got := filepath.Dir(pkg.report.MemPath); got != wantDir {
			t.Errorf("%s: MemPath dir = %s, want %s", pkg.name, got, wantDir)
		}

		cpuInfo, err := os.Stat(pkg.report.CPUPath)
		if err != nil {
			t.Errorf("%s: CPU profile file does not exist: %v", pkg.name, err)
		} else if cpuInfo.Size() == 0 {
			t.Errorf("%s: CPU profile file is empty", pkg.name)
		}

		memInfo, err := os.Stat(pkg.report.MemPath)
		if err != nil {
			t.Errorf("%s: memory profile file does not exist: %v", pkg.name, err)
		} else if memInfo.Size() == 0 {
			t.Errorf("%s: memory profile file is empty", pkg.name)
		}
	}

	// The two packages' profile files must be distinct: this is what
	// "does not overwrite" actually means in practice.
	if pkgA.CPUPath == pkgB.CPUPath {
		t.Errorf("pkga and pkgb share the same CPUPath: %s", pkgA.CPUPath)
	}
	if pkgA.MemPath == pkgB.MemPath {
		t.Errorf("pkga and pkgb share the same MemPath: %s", pkgA.MemPath)
	}
}

// TestDirsFiltersByConfiguredBenchmarks verifies that Dirs only returns the
// directories of benchmarks named in cfg.Benchmarks, and fails clearly when
// none match.
func TestDirsFiltersByConfiguredBenchmarks(t *testing.T) {
	root := writeMultiPackageFixture(t)

	dirs, err := Dirs(root, []string{"BenchmarkSum"})
	if err != nil {
		t.Fatalf("Dirs failed: %v", err)
	}
	if want := []string{"pkga"}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("Dirs = %v, want %v", dirs, want)
	}

	_, err = Dirs(root, []string{"BenchmarkDoesNotExist"})
	if err == nil {
		t.Fatal("Dirs succeeded for a benchmark name that does not exist")
	}
	if !strings.Contains(err.Error(), "BenchmarkDoesNotExist") {
		t.Errorf("Dirs error does not name the configured benchmark: %v", err)
	}
}

// assertProfileBlamesCountWords checks that the captured profile puts the
// fixture's own function on the hot path.
//
// It re-runs pprof with -cum rather than asserting on pkg.CPUTop, and the
// distinction is the whole point: Capture renders `-top -nodecount=15`,
// which is FLAT-ordered — the 15 hottest LEAF functions. CountWords spends
// almost all of its time inside runtime.concatstrings, so its flat time is
// small and whether it lands in the top 15 depends on how many distinct
// runtime leaves the sampler happened to catch. That made the old assertion
// a coin flip that got worse as the profile got better: at 300ms of samples
// the flat table filled up with runtime.kevent, mallocgc and memmove and
// CountWords dropped off entirely.
//
// Cumulatively it is not a coin flip at all — CountWords is an ancestor of
// every sample the benchmark takes, so it sits at 100% cum. That is the
// property worth asserting: the profile identifies the code under test.
// Go profiles carry their own symbols, so pprof needs no binary here, which
// matters because Capture deletes the test binary before returning.
func assertProfileBlamesCountWords(t *testing.T, cpuPath string) {
	t.Helper()
	out, err := exec.Command("go", "tool", "pprof", "-top", "-cum", "-nodecount=10", cpuPath).CombinedOutput()
	if err != nil {
		t.Fatalf("pprof -cum %s: %v\n%s", cpuPath, err, out)
	}
	if !strings.Contains(string(out), "CountWords") {
		t.Errorf("the captured profile does not put CountWords on the hot path:\n%s", out)
	}
}

// minCPUSamples is the number of CPU samples below which "what is in this
// profile?" stops being a question about the fixture and becomes a question
// about scheduling luck.
//
// Go's CPU profiler samples at 100 Hz — one sample per 10ms of on-CPU time.
// A 20ms benchtime yielded 3 samples on an idle machine here, and a full
// parallel test run once produced a table built from 2, both of them
// runtime.madvise: a single GC burst at the wrong instant displaced the
// benchmark entirely. The real profile command runs at the configured
// benchtime (default 1s, ~100 samples), so this is a property of the test,
// not of Capture.
const minCPUSamples = 10

// assertEnoughCPUSamples fails if the profile behind top is too thin for any
// assertion about its contents to mean anything, naming the sample count so
// the failure explains itself.
func assertEnoughCPUSamples(t *testing.T, top string) {
	t.Helper()
	m := totalSamplesRe.FindStringSubmatch(top)
	if m == nil {
		t.Fatalf("no %q line in pprof output — cannot tell whether the profile is adequate:\n%s",
			"Total samples =", top)
	}
	d, err := time.ParseDuration(m[1])
	if err != nil {
		t.Fatalf("parse total samples %q: %v", m[1], err)
	}
	if n := int(d / (10 * time.Millisecond)); n < minCPUSamples {
		t.Fatalf("profile has only ~%d CPU samples (total %s); at least %d are needed before the "+
			"contents of the top table say anything about the benchmark rather than about what else "+
			"the machine was doing. Raise the benchtime passed to Capture.\n%s", n, m[1], minCPUSamples, top)
	}
}

// totalSamplesRe matches pprof's "Total samples = 360ms (70.71%)" header.
var totalSamplesRe = regexp.MustCompile(`Total samples = ([0-9.]+(?:ns|us|µs|ms|s))`)
