package state

import (
	"path/filepath"
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
