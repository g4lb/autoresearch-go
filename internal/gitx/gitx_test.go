package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// repo creates a git repository with one commit and returns its path.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Gal Be")
	run("config", "user.email", "galevgi@gmail.com")

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

func TestHeadCommitAndBranch(t *testing.T) {
	dir := repo(t)

	c, err := HeadCommit(dir)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if len(c) != 7 {
		t.Errorf("HeadCommit = %q, want 7 chars", c)
	}

	b, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if b != "main" {
		t.Errorf("CurrentBranch = %q, want main", b)
	}
}

func TestBranchLifecycle(t *testing.T) {
	dir := repo(t)

	ok, err := BranchExists(dir, "autoresearch-go/sep4")
	if err != nil || ok {
		t.Fatalf("BranchExists = %v, %v; want false, nil", ok, err)
	}
	if err := CreateBranch(dir, "autoresearch-go/sep4"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	ok, err = BranchExists(dir, "autoresearch-go/sep4")
	if err != nil || !ok {
		t.Fatalf("BranchExists = %v, %v; want true, nil", ok, err)
	}
}

func TestIsCleanAndChangedSince(t *testing.T) {
	dir := repo(t)
	base, _ := HeadCommit(dir)

	clean, err := IsClean(dir)
	if err != nil || !clean {
		t.Fatalf("IsClean = %v, %v; want true, nil", clean, err)
	}

	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a // edited\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package a\n"), 0o644)

	clean, _ = IsClean(dir)
	if clean {
		t.Error("IsClean = true after edits")
	}

	changed, err := ChangedSince(dir, base)
	if err != nil {
		t.Fatalf("ChangedSince: %v", err)
	}
	want := map[string]bool{"a.go": true, "new.go": true}
	if len(changed) != 2 {
		t.Fatalf("ChangedSince = %v, want a.go and new.go", changed)
	}
	for _, c := range changed {
		if !want[c] {
			t.Errorf("unexpected changed file %q", c)
		}
	}
}

func TestChangedSinceNonASCII(t *testing.T) {
	dir := repo(t)
	base, _ := HeadCommit(dir)

	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "café.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := ChangedSince(dir, base)
	if err != nil {
		t.Fatalf("ChangedSince: %v", err)
	}
	if len(changed) != 1 || changed[0] != "internal/café.go" {
		t.Fatalf("ChangedSince = %v, want [\"internal/café.go\"] unquoted and unescaped", changed)
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	dir := repo(t)
	commit, _ := HeadCommit(dir)
	wt := filepath.Join(t.TempDir(), "baseline")

	if err := AddWorktree(dir, wt, commit); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.go")); err != nil {
		t.Fatalf("worktree missing a.go: %v", err)
	}
	if err := RemoveWorktree(dir, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still present after removal")
	}
}
