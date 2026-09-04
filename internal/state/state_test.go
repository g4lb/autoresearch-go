package state

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBaselineRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	want := &Baseline{
		Tag:          "sep4",
		Branch:       "autoresearch-go/sep4",
		Commit:       "a1b2c3d",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		Benchmarks:   []string{"BenchmarkCountWords"},
		Pattern:      "^(BenchmarkCountWords)$",
		ConfigSHA256: "deadbeef",
	}
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != want.Commit || got.Pattern != want.Pattern || len(got.Benchmarks) != 1 {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	if got.ConfigSHA256 != want.ConfigSHA256 {
		t.Errorf("ConfigSHA256 = %q, want %q", got.ConfigSHA256, want.ConfigSHA256)
	}
}

func TestLoadBaselineMissingIsActionable(t *testing.T) {
	_, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("LoadBaseline = nil error, want error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "baseline") {
		t.Errorf("error %q should mention running baseline first", got)
	}
}

func TestBenchPattern(t *testing.T) {
	if got := BenchPattern(nil); got != "." {
		t.Errorf("BenchPattern(nil) = %q, want %q", got, ".")
	}
	if got := BenchPattern([]string{"A", "B"}); got != "^(A|B)$" {
		t.Errorf("BenchPattern([A B]) = %q, want %q", got, "^(A|B)$")
	}
}

func TestBenchPatternEscapesMetacharacters(t *testing.T) {
	names := []string{"Benchmark.Foo", "Bar+Baz"}
	got := BenchPattern(names)
	want := "^(" + regexp.QuoteMeta(names[0]) + "|" + regexp.QuoteMeta(names[1]) + ")$"
	if got != want {
		t.Errorf("BenchPattern(%v) = %q, want %q", names, got, want)
	}

	re, err := regexp.Compile(got)
	if err != nil {
		t.Fatalf("BenchPattern produced an invalid regexp %q: %v", got, err)
	}
	// An unescaped "." in "Benchmark.Foo" would act as a wildcard and match
	// "BenchmarkXFoo" too. It must not.
	if re.MatchString("BenchmarkXFoo") {
		t.Errorf("pattern %q matched BenchmarkXFoo; %q must be escaped", got, names[0])
	}
	if !re.MatchString("Benchmark.Foo") {
		t.Errorf("pattern %q should still match the literal name %q", got, names[0])
	}
}

func TestStateDirResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically needs elevation on Windows")
	}
	real := t.TempDir()
	base := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	dReal, err := StateDir(real, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	dLink, err := StateDir(link, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	if dReal != dLink {
		t.Errorf("StateDir(%q) = %q, StateDir(%q) = %q; want the same path", real, dReal, link, dLink)
	}
}

func TestStateDirOutOfTreeAndDeterministic(t *testing.T) {
	repo1 := t.TempDir()
	repo2 := t.TempDir()

	d1, err := StateDir(repo1, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	d1again, err := StateDir(repo1, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d1again {
		t.Errorf("StateDir not deterministic: %q != %q", d1, d1again)
	}

	d2, err := StateDir(repo2, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Errorf("two different repos produced the same state dir %q", d1)
	}

	if strings.HasPrefix(d1, repo1) {
		t.Errorf("StateDir %q must not live inside the repository %q", d1, repo1)
	}

	dTag2, err := StateDir(repo1, "oct1")
	if err != nil {
		t.Fatal(err)
	}
	if dTag2 == d1 {
		t.Errorf("two different tags for the same repo produced the same state dir %q", d1)
	}
}
