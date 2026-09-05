// Package freeze snapshots test files at baseline time and restores them
// before every evaluation, so an agent cannot weaken its own success criteria.
package freeze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Locations relative to the run's out-of-tree state.StateDir — never to the
// repository root. The frozen store and its manifest are part of what the
// success metric depends on, so they must live where the agent being
// measured cannot reach them; a caller that joins these onto the repository
// root instead would silently reintroduce that hole.
const (
	StoreDir     = "frozen"
	ManifestPath = "frozen/manifest.json"
)

// Manifest maps repo-relative file paths to their sha256 at baseline time.
type Manifest struct {
	Files map[string]string `json:"files"`
}

// ErrSymlink indicates a frozen test file's path is a symlink. Both
// Snapshot and Restore use os.WriteFile / os.ReadFile under the hood, which
// follow symlinks: writing to a symlinked destination writes through it to
// wherever it points, potentially outside the repository entirely. Rather
// than follow the link, Snapshot and Restore refuse and return an error
// wrapping ErrSymlink, so callers can distinguish "tampering detected" from
// an ordinary I/O failure.
var ErrSymlink = errors.New("frozen test file path is a symlink")

// lstatIsSymlink reports whether path exists and is a symlink, using Lstat
// (not Stat) so the check is about the path itself, not whatever it points
// to. Stat would follow the link and report the target's mode, silently
// defeating the check this exists to make. A path that does not exist is
// not a symlink and is left to the caller's own not-exist handling.
func lstatIsSymlink(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// safeJoin joins rel onto root, rejecting anything that would escape it.
// Manifest entries come from a JSON file on disk, so they are untrusted
// input: Restore writes through them before every evaluation.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("frozen path %q must be relative", rel)
	}
	clean := filepath.Clean(filepath.ToSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("frozen path %q escapes the repository root", rel)
	}
	return filepath.Join(root, clean), nil
}

// Snapshot copies each file into storeDir and records its hash.
// Paths are repo-relative and are preserved inside the store.
func Snapshot(repoRoot, storeDir string, files []string) (*Manifest, error) {
	m := &Manifest{Files: map[string]string{}}
	for _, rel := range files {
		src, err := safeJoin(repoRoot, rel)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
		if isLink, err := lstatIsSymlink(src); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		} else if isLink {
			return nil, fmt.Errorf("snapshot %s: %w: symlinked test files are unsupported "+
				"because the harness cannot guarantee restoring them stays inside the repository",
				rel, ErrSymlink)
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
		dst, err := safeJoin(storeDir, rel)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", rel, err)
		}
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
		src, err := safeJoin(storeDir, rel)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		want, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		dst, err := safeJoin(repoRoot, rel)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		}
		if isLink, err := lstatIsSymlink(dst); err != nil {
			return nil, fmt.Errorf("restore %s: %w", rel, err)
		} else if isLink {
			return nil, fmt.Errorf("restore %s: %w: a frozen test file was replaced by a "+
				"symlink; refusing to write through it, which could reach a file outside the "+
				"repository", rel, ErrSymlink)
		}
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
		path, err := safeJoin(repoRoot, rel)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", rel, err)
		}
		if isLink, err := lstatIsSymlink(path); err != nil {
			return nil, fmt.Errorf("verify %s: %w", rel, err)
		} else if isLink {
			// A frozen path that is now a symlink is at least as suspicious
			// as a deleted one — report it as changed rather than following
			// the link to read whatever it points at.
			changed = append(changed, rel)
			continue
		}
		b, err := os.ReadFile(path)
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
