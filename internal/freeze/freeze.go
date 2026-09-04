// Package freeze snapshots test files at baseline time and restores them
// before every evaluation, so an agent cannot weaken its own success criteria.
package freeze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Locations relative to the repository root.
const (
	StoreDir     = ".autoresearch/frozen"
	ManifestPath = ".autoresearch/frozen/manifest.json"
)

// Manifest maps repo-relative file paths to their sha256 at baseline time.
type Manifest struct {
	Files map[string]string `json:"files"`
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Snapshot copies each file into storeDir and records its hash.
// Paths are repo-relative and are preserved inside the store.
func Snapshot(repoRoot, storeDir string, files []string) (*Manifest, error) {
	m := &Manifest{Files: map[string]string{}}
	for _, rel := range files {
		src := filepath.Join(repoRoot, rel)
		b, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
		dst := filepath.Join(storeDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
		m.Files[rel] = hashBytes(b)
	}
	return m, nil
}

// Restore rewrites every frozen file in the working tree from the store,
// recreating files the agent deleted. It returns the paths it changed.
func Restore(repoRoot, storeDir string, m *Manifest) ([]string, error) {
	var changed []string
	for _, rel := range m.sortedPaths() {
		src := filepath.Join(storeDir, rel)
		want, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		dst := filepath.Join(repoRoot, rel)
		if got, err := os.ReadFile(dst); err == nil && hashBytes(got) == hashBytes(want) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		changed = append(changed, rel)
	}
	return changed, nil
}

// Verify reports which frozen files currently differ from the baseline.
// A deleted file counts as changed.
func Verify(repoRoot string, m *Manifest) ([]string, error) {
	var changed []string
	for _, rel := range m.sortedPaths() {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if os.IsNotExist(err) {
			changed = append(changed, rel)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", rel, err)
		}
		if hashBytes(b) != m.Files[rel] {
			changed = append(changed, rel)
		}
	}
	return changed, nil
}

func (m *Manifest) sortedPaths() []string {
	out := make([]string, 0, len(m.Files))
	for p := range m.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Save writes the manifest as indented JSON.
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// LoadManifest reads a manifest written by Save.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return &m, nil
}
