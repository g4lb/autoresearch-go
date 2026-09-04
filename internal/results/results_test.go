package results

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendWritesHeaderOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "results.tsv")

	if err := Append(p, Row{Commit: "a1b2c3d", Score: 1.0, Status: "keep", Description: "baseline"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(p, Row{Commit: "b2c3d4e", Score: 0.91, Status: "keep", Description: "prealloc slice"}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows):\n%s", len(lines), b)
	}
	if lines[0] != Header {
		t.Errorf("header = %q, want %q", lines[0], Header)
	}
	if strings.Count(string(b), Header) != 1 {
		t.Error("header written more than once")
	}
}

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "results.tsv")
	want := Row{Commit: "a1b2c3d", Score: 0.9213, BestBenchDelta: -21.4, AllocsDelta: -3, Status: "keep", Description: "avoid byte copy"}
	if err := Append(p, want); err != nil {
		t.Fatal(err)
	}

	rows, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("Load = %d rows, want 1", len(rows))
	}
	if rows[0] != want {
		t.Errorf("Load = %+v, want %+v", rows[0], want)
	}
}

func TestDescriptionCannotBreakTheFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "results.tsv")
	if err := Append(p, Row{Commit: "c", Status: "keep", Description: "one\ttwo\nthree"}); err != nil {
		t.Fatal(err)
	}
	rows, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("Load = %d rows, want 1", len(rows))
	}
	if strings.ContainsAny(rows[0].Description, "\t\n") {
		t.Errorf("Description = %q, still contains a separator", rows[0].Description)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	rows, err := Load(filepath.Join(t.TempDir(), "nope.tsv"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Load = %v, want empty", rows)
	}
}

func TestAppendReportsWriteErrors(t *testing.T) {
	// Append to a path inside a nonexistent directory.
	p := filepath.Join(t.TempDir(), "nonexistent-dir", "results.tsv")
	err := Append(p, Row{Commit: "abc", Status: "keep", Description: "test"})
	if err == nil {
		t.Fatal("Append: expected an error for nonexistent directory, got nil")
	}
	// Error should mention the path so the user can debug it.
	if !strings.Contains(err.Error(), p) {
		t.Errorf("error = %v, does not mention path %q", err, p)
	}
}
