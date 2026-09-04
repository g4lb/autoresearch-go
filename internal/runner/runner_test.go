package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tinyModule writes a module that builds, vets and tests cleanly.
func tinyModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module tiny\n\ngo 1.21\n")
	write("tiny.go", "package tiny\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n")
	write("tiny_test.go", `package tiny

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}

func BenchmarkAdd(b *testing.B) {
	sink := 0
	for i := 0; i < b.N; i++ {
		sink = Add(i, 1)
	}
	_ = sink
}
`)
	return dir
}

func TestBuildVetTestSucceed(t *testing.T) {
	var log bytes.Buffer
	r := New(tinyModule(t), 2*time.Minute, &log)
	ctx := context.Background()

	for name, fn := range map[string]func() (*Result, error){
		"build": func() (*Result, error) { return r.Build(ctx) },
		"vet":   func() (*Result, error) { return r.Vet(ctx) },
		"test":  func() (*Result, error) { return r.Test(ctx, false) },
	} {
		res, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !res.OK() {
			t.Fatalf("%s failed: exit=%d\n%s", name, res.ExitCode, res.Tail(20))
		}
	}
	if log.Len() == 0 {
		t.Error("nothing written to the log")
	}
}

func TestBenchProducesParseableOutput(t *testing.T) {
	r := New(tinyModule(t), 2*time.Minute, nil)
	res, err := r.Bench(context.Background(), "^BenchmarkAdd$", "10ms")
	if err != nil {
		t.Fatalf("Bench: %v", err)
	}
	if !res.OK() {
		t.Fatalf("bench failed: %s", res.Tail(20))
	}
	if !strings.Contains(string(res.Stdout), "BenchmarkAdd") {
		t.Fatalf("stdout lacks the benchmark line:\n%s", res.Stdout)
	}
	if !strings.Contains(string(res.Stdout), "ns/op") {
		t.Fatalf("stdout lacks ns/op:\n%s", res.Stdout)
	}
}

func TestBuildFailureIsReportedNotReturnedAsError(t *testing.T) {
	dir := tinyModule(t)
	os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package tiny\n\nthis is not go\n"), 0o644)

	res, err := New(dir, time.Minute, nil).Build(context.Background())
	if err != nil {
		t.Fatalf("Build returned a Go error for a compile failure: %v", err)
	}
	if res.OK() {
		t.Fatal("OK() = true for a broken build")
	}
	if res.ExitCode == 0 {
		t.Error("ExitCode = 0 for a broken build")
	}
}

func TestTimeoutIsFlagged(t *testing.T) {
	r := New(tinyModule(t), 1*time.Millisecond, nil)
	res, err := r.Test(context.Background(), false)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, want true (exit=%d)", res.ExitCode)
	}
}

func TestCapWriterBounds(t *testing.T) {
	w := &capWriter{limit: 100}

	// Write less than limit
	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if w.truncated {
		t.Error("truncated after small write")
	}

	// Write to exceed limit
	n, err = w.Write(make([]byte, 96)) // Total would be 101
	if err != nil || n != 96 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if !w.truncated {
		t.Error("truncated not set after exceeding limit")
	}

	// Verify buffer size is capped
	if w.buf.Len() > 100 {
		t.Errorf("buffer size %d exceeds limit 100", w.buf.Len())
	}

	// Write more after truncated
	n, err = w.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("Write after cap: n=%d err=%v", n, err)
	}
	if w.buf.Len() > 100 {
		t.Errorf("buffer still capped at %d", w.buf.Len())
	}
}

func TestTailEdgeCases(t *testing.T) {
	for _, tt := range []struct {
		name      string
		stderr    string
		stdout    string
		n         int
		wantLines int
	}{
		{"empty", "", "", 5, 0},
		{"single_line", "hello", "", 1, 1},
		{"single_line_n_zero", "hello", "", 0, 0},
		{"single_line_negative_n", "hello", "", -1, 0},
		{"multi_line_n_negative", "a\nb\nc", "", -5, 0},
		{"multi_line_n_exceeds", "a\nb\nc", "", 10, 3},
		{"fallback_stdout", "", "line1\nline2", 1, 1},
		{"stderr_precedence", "err1\nerr2", "out1", 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := &Result{
				Stderr: []byte(tt.stderr),
				Stdout: []byte(tt.stdout),
			}
			tail := res.Tail(tt.n)
			lines := strings.Split(strings.TrimSpace(tail), "\n")
			if strings.TrimSpace(tail) == "" {
				lines = nil
			}
			if len(lines) != tt.wantLines {
				t.Errorf("got %d lines, want %d; tail=%q", len(lines), tt.wantLines, tail)
			}
		})
	}
}
