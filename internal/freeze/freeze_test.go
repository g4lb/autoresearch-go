package freeze

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// setup builds a fake repo with one frozen test file and returns root, store.
func setup(t *testing.T) (string, string, *Manifest) {
	t.Helper()
	root := t.TempDir()
	store := filepath.Join(root, StoreDir)
	writeFile(t, filepath.Join(root, "pkg/a_test.go"), "package pkg // original\n")

	m, err := Snapshot(root, store, []string{"pkg/a_test.go"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return root, store, m
}

func TestSnapshotRecordsHash(t *testing.T) {
	_, _, m := setup(t)
	if len(m.Files) != 1 {
		t.Fatalf("Files = %v, want 1 entry", m.Files)
	}
	if h := m.Files["pkg/a_test.go"]; len(h) != 64 {
		t.Errorf("hash = %q, want 64 hex chars", h)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	root, _, m := setup(t)

	changed, err := Verify(root, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("Verify on pristine tree = %v, want none", changed)
	}

	writeFile(t, filepath.Join(root, "pkg/a_test.go"), "package pkg // WEAKENED\n")
	changed, err = Verify(root, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(changed) != 1 || changed[0] != "pkg/a_test.go" {
		t.Errorf("changed = %v, want [pkg/a_test.go]", changed)
	}
}

func TestRestoreErasesAgentEdits(t *testing.T) {
	root, store, m := setup(t)
	writeFile(t, filepath.Join(root, "pkg/a_test.go"), "package pkg // WEAKENED\n")

	restored, err := Restore(root, store, m)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored) != 1 {
		t.Errorf("restored = %v, want 1 file", restored)
	}
	if got := readFile(t, filepath.Join(root, "pkg/a_test.go")); got != "package pkg // original\n" {
		t.Errorf("content = %q, want the original", got)
	}
}

func TestRestoreRecreatesDeletedFile(t *testing.T) {
	root, store, m := setup(t)
	os.Remove(filepath.Join(root, "pkg/a_test.go"))

	if _, err := Restore(root, store, m); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "pkg/a_test.go")); got != "package pkg // original\n" {
		t.Errorf("content = %q, want the original restored", got)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	root, _, m := setup(t)
	p := filepath.Join(root, "manifest.json")
	if err := m.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.Files["pkg/a_test.go"] != m.Files["pkg/a_test.go"] {
		t.Errorf("round trip lost the hash")
	}
}

func TestVerifyDetectsDeletion(t *testing.T) {
	// Deleting a test file is the simplest possible attack. Verify must count
	// it as changed, not skip it as absent.
	root, _, m := setup(t)
	os.Remove(filepath.Join(root, "pkg/a_test.go"))

	changed, err := Verify(root, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(changed) != 1 || changed[0] != "pkg/a_test.go" {
		t.Errorf("changed = %v, want [pkg/a_test.go] for a deleted file", changed)
	}
}

func TestSnapshotPreservesPackagePaths(t *testing.T) {
	// Two test files sharing a base name in different packages must not
	// collide inside the store. If the store flattened paths, one would
	// overwrite the other and Restore would hand back the wrong content.
	root := t.TempDir()
	store := filepath.Join(root, StoreDir)
	writeFile(t, filepath.Join(root, "internal/a/x_test.go"), "package a // A\n")
	writeFile(t, filepath.Join(root, "internal/b/x_test.go"), "package b // B\n")

	m, err := Snapshot(root, store, []string{"internal/a/x_test.go", "internal/b/x_test.go"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if m.Files["internal/a/x_test.go"] == m.Files["internal/b/x_test.go"] {
		t.Fatal("both files hashed identically; the snapshot collapsed them")
	}

	// Tamper with both, then restore.
	writeFile(t, filepath.Join(root, "internal/a/x_test.go"), "package a // TAMPERED\n")
	writeFile(t, filepath.Join(root, "internal/b/x_test.go"), "package b // TAMPERED\n")
	if _, err := Restore(root, store, m); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := readFile(t, filepath.Join(root, "internal/a/x_test.go")); got != "package a // A\n" {
		t.Errorf("internal/a restored to %q, want its own original content", got)
	}
	if got := readFile(t, filepath.Join(root, "internal/b/x_test.go")); got != "package b // B\n" {
		t.Errorf("internal/b restored to %q, want its own original content", got)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	// Snapshot, Restore, and Verify must reject paths that escape the root.
	// A corrupted or hostile manifest turns restore into an arbitrary-file-write.

	root := t.TempDir()
	store := filepath.Join(root, StoreDir)

	// Test 1: Snapshot rejects ../escape
	_, err := Snapshot(root, store, []string{"../escape_test.go"})
	if err == nil {
		t.Errorf("Snapshot with ../escape_test.go should error, got nil")
	}

	// Test 2: Restore rejects ../escape and does not create file outside root
	escapeManifest := &Manifest{Files: map[string]string{"../escape_test.go": "deadbeef"}}
	_, err = Restore(root, store, escapeManifest)
	if err == nil {
		t.Errorf("Restore with ../escape_test.go should error, got nil")
	}
	// Verify no file was created outside root
	escapeFile := filepath.Join(filepath.Dir(root), "escape_test.go")
	if _, err := os.Stat(escapeFile); !os.IsNotExist(err) {
		t.Errorf("Restore created file outside root at %s", escapeFile)
	}

	// Test 3: Verify rejects ../escape
	_, err = Verify(root, escapeManifest)
	if err == nil {
		t.Errorf("Verify with ../escape_test.go should error, got nil")
	}

	// Test 4: Snapshot rejects absolute paths
	_, err = Snapshot(root, store, []string{"/tmp/escape_test.go"})
	if err == nil {
		t.Errorf("Snapshot with absolute path should error, got nil")
	}

	// Test 5: Restore rejects absolute paths and does not create file
	absManifest := &Manifest{Files: map[string]string{"/tmp/evil_test.go": "deadbeef"}}
	_, err = Restore(root, store, absManifest)
	if err == nil {
		t.Errorf("Restore with absolute path should error, got nil")
	}
}

// TestRestoreRefusesToWriteThroughSymlink reproduces the serious attack: the
// agent removes a normally-frozen file and replaces it with a symlink to a
// file outside the repository. os.WriteFile follows symlinks, so a naive
// Restore would write the frozen test's content through the link, clobbering
// whatever it points at. Restore must refuse, and — this is the part an
// error-after-writing implementation would get wrong — the outside file's
// content must be completely untouched.
func TestRestoreRefusesToWriteThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically needs elevation on Windows")
	}
	root, store, m := setup(t)

	// A file in a wholly separate temp directory: the "any user file"
	// outside the repository that Path B targets.
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	const victimContent = "do not touch this file\n"
	writeFile(t, victim, victimContent)

	// The agent mid-run: delete the frozen file, put a symlink in its place.
	testFile := filepath.Join(root, "pkg/a_test.go")
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, testFile); err != nil {
		t.Fatal(err)
	}

	_, err := Restore(root, store, m)
	if err == nil {
		t.Fatal("Restore through a symlink should error, got nil")
	}
	if !errors.Is(err, ErrSymlink) {
		t.Errorf("err = %v, want it to wrap ErrSymlink", err)
	}

	// The load-bearing assertion: the outside file must be byte-for-byte
	// unchanged. An implementation that writes first and errors afterward
	// would pass an error-only check while still destroying this file.
	got := readFile(t, victim)
	if got != victimContent {
		t.Errorf("outside file content = %q, want unchanged %q", got, victimContent)
	}
}

// TestSnapshotRefusesSymlinkedTestFile reproduces Path A: a _test.go that is
// already a symlink at baseline time. Snapshot must refuse and name the
// offending path, catching this before the run ever starts rather than on
// every eval afterward.
func TestSnapshotRefusesSymlinkedTestFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically needs elevation on Windows")
	}
	root := t.TempDir()
	store := filepath.Join(root, StoreDir)

	outside := t.TempDir()
	target := filepath.Join(outside, "target.go")
	writeFile(t, target, "package pkg // target\n")

	linkPath := filepath.Join(root, "pkg/link_test.go")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	_, err := Snapshot(root, store, []string{"pkg/link_test.go"})
	if err == nil {
		t.Fatal("Snapshot of a symlinked test file should error, got nil")
	}
	if !errors.Is(err, ErrSymlink) {
		t.Errorf("err = %v, want it to wrap ErrSymlink", err)
	}
	if !strings.Contains(err.Error(), "pkg/link_test.go") {
		t.Errorf("err = %v, want it to name the offending path", err)
	}
}

// TestVerifyReportsSymlinkedPathAsChanged asserts Verify treats a frozen
// path that has become a symlink as changed, the same way it already treats
// a deleted file — Verify is what surfaces tampering, so it must not stay
// silent about this route.
func TestVerifyReportsSymlinkedPathAsChanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically needs elevation on Windows")
	}
	root, _, m := setup(t)

	// The link's target holds byte-for-byte the same content setup() froze.
	// If Verify fell back to a plain content comparison instead of checking
	// for a symlink, it would read through the link, see matching content,
	// and wrongly call this path unchanged — this must be caught by the
	// symlink check itself, not incidentally by a content mismatch.
	outside := t.TempDir()
	target := filepath.Join(outside, "target.txt")
	writeFile(t, target, "package pkg // original\n")

	testFile := filepath.Join(root, "pkg/a_test.go")
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, testFile); err != nil {
		t.Fatal(err)
	}

	changed, err := Verify(root, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(changed) != 1 || changed[0] != "pkg/a_test.go" {
		t.Errorf("changed = %v, want [pkg/a_test.go] for a symlinked path", changed)
	}
}
