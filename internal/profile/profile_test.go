package profile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/g4lb/autoresearch-go/internal/runner"
)

// TestCaptureSinglePackage covers the common case: every declared benchmark
// lives in one package, so Capture must still write its profile files
// directly to dest (not into a per-package subdirectory), matching the
// behavior before multi-package support existed.
func TestCaptureSinglePackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// Run against testdata/demo with a short benchtime to keep the test fast.
	destDir := t.TempDir()
	ctx := context.Background()

	dirs, err := Dirs("../../testdata/demo", nil)
	if err != nil {
		t.Fatalf("Dirs failed: %v", err)
	}
	if want := []string{"."}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("Dirs = %v, want %v", dirs, want)
	}

	report, err := Capture(ctx, "../../testdata/demo", dirs, "Benchmark.*", "20ms", destDir, 30*time.Second)
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
	if !strings.Contains(pkg.CPUTop, "CountWords") {
		t.Errorf("CPUTop does not mention CountWords:\n%s", pkg.CPUTop)
	}

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

	// Mutation evidence: prove the fixture actually bites go test's own
	// restriction, so a regression back to `go test ./...` would be caught.
	// See docs/superpowers/run-log/profile-multipackage-fix.md for the
	// recorded before/after run of this same assertion against Capture.
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
