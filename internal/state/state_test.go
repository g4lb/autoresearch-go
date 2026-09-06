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

func TestBaselineMeasureCommitRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	want := &Baseline{
		Tag:           "sep4",
		Branch:        "autoresearch-go/sep4",
		Commit:        "a1b2c3d",
		MeasureCommit: "e4f5a6b", // advanced past Commit by a KEEP
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		Benchmarks:    []string{"BenchmarkCountWords"},
		Pattern:       "^(BenchmarkCountWords)$",
		ConfigSHA256:  "deadbeef",
	}
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.MeasureCommit != want.MeasureCommit {
		t.Errorf("MeasureCommit = %q, want %q", got.MeasureCommit, want.MeasureCommit)
	}
	if got.Commit != want.Commit {
		t.Errorf("Commit = %q, want %q (must not be conflated with MeasureCommit)", got.Commit, want.Commit)
	}
}

// TestLoadBaselineDefaultsMeasureCommitToCommit covers a baseline.json
// written before MeasureCommit existed (no "measure_commit" key at all): it
// must fall back to Commit rather than loading as "", which would fail the
// worktree integrity check in internal/pipeline on the very first eval of
// an old run.
func TestLoadBaselineDefaultsMeasureCommitToCommit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	const oldFormat = `{
  "tag": "sep4",
  "branch": "autoresearch-go/sep4",
  "commit": "a1b2c3d",
  "created_at": "2026-01-01T00:00:00Z",
  "benchmarks": ["BenchmarkCountWords"],
  "pattern": "^(BenchmarkCountWords)$",
  "config_sha256": "deadbeef"
}
`
	if err := os.WriteFile(p, []byte(oldFormat), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBaseline(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.MeasureCommit != got.Commit {
		t.Errorf("MeasureCommit = %q, want it defaulted to Commit %q for a pre-MeasureCommit baseline.json",
			got.MeasureCommit, got.Commit)
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

func TestValidTagRejectsTraversalAndSeparators(t *testing.T) {
	bad := []string{"..", "../escape", "a/b", "/etc/passwd", ".", ""}
	for _, tag := range bad {
		t.Run(tag, func(t *testing.T) {
			if err := ValidTag(tag); err == nil {
				t.Errorf("ValidTag(%q) = nil, want error", tag)
			}
		})
	}
}

func TestValidTagAcceptsLegalTags(t *testing.T) {
	good := []string{"sep4", "oct1", "release-1.2.3", "a_b", "2026-09-03", "A"}
	for _, tag := range good {
		t.Run(tag, func(t *testing.T) {
			if err := ValidTag(tag); err != nil {
				t.Errorf("ValidTag(%q) = %v, want nil", tag, err)
			}
		})
	}
}

func TestStateDirRejectsInvalidTag(t *testing.T) {
	if _, err := StateDir(t.TempDir(), "../escape"); err == nil {
		t.Fatal("StateDir with a traversal tag = nil error, want error")
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

func TestStateDirUsesTheStateHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(StateHomeEnv, home)

	dir, err := StateDir(t.TempDir(), "sep4")
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if !strings.HasPrefix(dir, home+string(filepath.Separator)) {
		t.Errorf("StateDir = %q, want it under the override %q", dir, home)
	}
	if filepath.Base(dir) != "sep4" {
		t.Errorf("StateDir = %q, want it to end in the run tag", dir)
	}
}

func TestStateDirFallsBackToTheUserCache(t *testing.T) {
	t.Setenv(StateHomeEnv, "")

	dir, err := StateDir(t.TempDir(), "sep4")
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on this platform: %v", err)
	}
	want := filepath.Join(cache, StateDirName)
	if !strings.HasPrefix(dir, want+string(filepath.Separator)) {
		t.Errorf("StateDir = %q, want it under %q when the override is unset", dir, want)
	}
}

func TestStateDirRejectsARelativeOverride(t *testing.T) {
	// A relative override would resolve against whatever directory the
	// process happens to be in, so the same run would resolve to different
	// state depending on where the command was invoked from.
	t.Setenv(StateHomeEnv, "relative/state")

	if _, err := StateDir(t.TempDir(), "sep4"); err == nil {
		t.Fatal("StateDir accepted a relative override; want an error")
	}
}

func TestStateDirKeepsRepositoriesApartUnderAnOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(StateHomeEnv, home)

	a, err := StateDir(t.TempDir(), "sep4")
	if err != nil {
		t.Fatal(err)
	}
	b, err := StateDir(t.TempDir(), "sep4")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("two repositories share a state dir under an override: %q", a)
	}
}
