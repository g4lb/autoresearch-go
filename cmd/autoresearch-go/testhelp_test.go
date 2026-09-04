package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit runs git in dir, failing the test on error. It never touches global
// git config: identity is always set repo-local by copyDemoRepo.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// copyDir recursively copies src onto dst, creating directories as needed.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read dir %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		copyFile(t, s, d)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", src, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// demoRepoSrc locates the shared testdata/demo fixture relative to this
// package, which lives at cmd/autoresearch-go.
func demoRepoSrc(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("testdata/demo fixture missing: %v", err)
	}
	return dir
}

// copyDemoRepo copies testdata/demo into a fresh temp directory, git-inits
// it with a repo-LOCAL identity (never --global, so this never touches a
// developer's or CI runner's global git config), and commits it. It returns
// the new repository root.
func copyDemoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, demoRepoSrc(t), dir)

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Gal Be")
	runGit(t, dir, "config", "user.email", "galevgi@gmail.com")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "initial commit")
	return dir
}

// mustInit runs runInit against dir (via -C) and fails the test unless it
// succeeds. It is shared with later tasks (baseline, eval) that need an
// already-initialized repository to build on.
func mustInit(t *testing.T, dir string) {
	t.Helper()
	if code := runInit([]string{"-C", dir}); code != exitOK {
		t.Fatalf("runInit(-C %s) = %d, want %d", dir, code, exitOK)
	}
}
