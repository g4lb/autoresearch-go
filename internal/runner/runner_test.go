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
