package freeze

import (
	"os"
	"path/filepath"
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
