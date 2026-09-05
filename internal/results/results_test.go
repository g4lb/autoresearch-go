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

func TestAppendTruncatesOverLongDescription(t *testing.T) {
	// An agent pasting something large (a stack trace, a diff) into -desc
	// must not be able to produce a results.tsv line long enough to jam
	// every future Load. Append caps it instead.
	p := filepath.Join(t.TempDir(), "results.tsv")
	huge := strings.Repeat("x", 10_000)
	if err := Append(p, Row{Commit: "a1b2c3d", Status: "keep", Description: huge}); err != nil {
		t.Fatal(err)
	}

	rows, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Load = %d rows, want 1", len(rows))
	}
	got := rows[0].Description
	if len(got) >= len(huge) {
		t.Fatalf("Description length = %d, want it truncated well below the original %d", len(got), len(huge))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("Description = %q, want it to end with an ellipsis marking truncation", got)
	}
	if !strings.HasPrefix(huge, strings.TrimSuffix(got, "...")) {
		t.Errorf("Description = %q, want a prefix of the original description", got)
	}
}

func TestAppendDoesNotTruncateADescriptionAtTheCap(t *testing.T) {
	// A description at or under the cap must round-trip byte-for-byte,
	// with no ellipsis appended.
	p := filepath.Join(t.TempDir(), "results.tsv")
	short := "avoid byte copy in the hot loop"
	if err := Append(p, Row{Commit: "a1b2c3d", Status: "keep", Description: short}); err != nil {
		t.Fatal(err)
	}
	rows, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rows) != 1 || rows[0].Description != short {
		t.Fatalf("Load = %+v, want Description %q untouched", rows, short)
	}
}

func TestLoadToleratesLineOverDefaultScannerLimit(t *testing.T) {
	// bufio.Scanner's default token limit is 64 KiB. A hand-edited line
	// bigger than that but still under Load's raised buffer must load
	// successfully, not trip bufio.ErrTooLong.
	p := filepath.Join(t.TempDir(), "results.tsv")
	big := strings.Repeat("z", 200*1024) // well over 64 KiB, well under maxScanTokenSize
	content := Header + "\n" + "a1b2c3d\t1.0000\t0.00\t0.00\tkeep\t" + big + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rows, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v, want the raised buffer to tolerate a line over the default 64 KiB limit", err)
	}
	if len(rows) != 1 || rows[0].Description != big {
		t.Fatalf("Load = %d row(s), want 1 with the full 200 KiB description intact", len(rows))
	}
}

func TestLoadOverLongLineReportsClearError(t *testing.T) {
	// A hand-written (or pre-fix) over-long line must produce an actionable
	// error naming the file and line, not a bare bufio.ErrTooLong.
	p := filepath.Join(t.TempDir(), "results.tsv")
	huge := strings.Repeat("y", maxScanTokenSize+1024)
	content := Header + "\n" + "a1b2c3d\t1.0000\t0.00\t0.00\tkeep\t" + huge + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(p)
	if err == nil {
		t.Fatal("Load = nil error, want an error for an over-long line")
	}
	msg := err.Error()
	if !strings.Contains(msg, p) {
		t.Errorf("error = %q, want it to name the file %q", msg, p)
	}
	if !strings.Contains(msg, "2") {
		t.Errorf("error = %q, want it to name line 2", msg)
	}
	if !strings.Contains(msg, "too long") {
		t.Errorf("error = %q, want a clear \"too long\" message, not a bare bufio.ErrTooLong", msg)
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
