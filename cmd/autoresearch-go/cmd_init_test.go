package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/config"
)

func TestInitWritesConfigAndProgram(t *testing.T) {
	dir := copyDemoRepo(t)

	if code := runInit([]string{"-C", dir}); code != exitOK {
		t.Fatalf("runInit = %d, want %d", code, exitOK)
	}
	cfg, err := config.Load(filepath.Join(dir, config.Path))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if len(cfg.Benchmarks) != 1 || cfg.Benchmarks[0] != "BenchmarkCountWords" {
		t.Errorf("Benchmarks = %v, want [BenchmarkCountWords]", cfg.Benchmarks)
	}
	if _, err := os.Stat(filepath.Join(dir, "program.md")); err != nil {
		t.Errorf("program.md not written: %v", err)
	}
}

func TestInitAppendsGitignoreWithoutDuplicating(t *testing.T) {
	dir := copyDemoRepo(t)
	// Pre-seed one of the four entries; init must not duplicate it, and
	// must still add the other three.
	giPath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(giPath, []byte("results.tsv\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runInit([]string{"-C", dir}); code != exitOK {
		t.Fatalf("runInit = %d, want %d", code, exitOK)
	}

	b, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range []string{".autoresearch/*", "!.autoresearch/config.yaml", "results.tsv", "run.log"} {
		if strings.Count(content, want) != 1 {
			t.Errorf(".gitignore contains %q %d times, want exactly 1\n%s", want, strings.Count(content, want), content)
		}
	}
}

func TestInitTracksConfigButIgnoresOtherHarnessOutput(t *testing.T) {
	// .autoresearch/ now holds only config.yaml, which humans own and want
	// version-controlled — the opposite of everything else the harness
	// writes there (e.g. Task 16's profile output). init's .gitignore must
	// reflect exactly that split.
	dir := copyDemoRepo(t)
	if code := runInit([]string{"-C", dir}); code != exitOK {
		t.Fatalf("runInit = %d, want %d", code, exitOK)
	}

	if gitIsIgnored(t, dir, ".autoresearch/config.yaml") {
		t.Error(".autoresearch/config.yaml is ignored, want it tracked")
	}
	if !gitIsIgnored(t, dir, ".autoresearch/profiles/cpu.out") {
		t.Error(".autoresearch/profiles/cpu.out is not ignored, want harness output under .autoresearch/ ignored")
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runInit([]string{"-C", dir}); code == exitOK {
		t.Fatal("second runInit succeeded, want refusal without -force")
	}
}

func TestInitForceOverwrites(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runInit([]string{"-C", dir, "-force"}); code != exitOK {
		t.Fatalf("runInit -force = %d, want %d", code, exitOK)
	}
}

func TestInitReportsNoBenchmarks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module nobench\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nobench.go"), []byte("package nobench\n\n// Add adds two ints.\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nobench_test.go"), []byte("package nobench\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Gal Be")
	runGit(t, dir, "config", "user.email", "galevgi@gmail.com")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "initial commit")

	if code := runInit([]string{"-C", dir}); code == exitOK {
		t.Fatal("runInit succeeded on a repo with no benchmarks, want a clear failure")
	}
	if _, err := os.Stat(filepath.Join(dir, config.Path)); err == nil {
		t.Error("runInit wrote a config despite having no benchmarks to measure")
	}
}

func TestInitRequiresGitRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	if code := runInit([]string{"-C", dir}); code == exitOK {
		t.Fatal("runInit succeeded outside a git repository, want a clear failure")
	}
}
