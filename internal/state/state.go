// Package state persists the baseline record for a run, out-of-tree from the
// repository being optimized.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StateDirName is the per-repository state directory name under the user cache.
const StateDirName = "autoresearch-go"

// StateDir returns the OUT-OF-TREE directory holding every piece of state the
// metric depends on, for one repository and run tag.
//
// NONE of this may live inside the repository. The agent edits the repository;
// `.autoresearch/` is gitignored so ChangedSince cannot observe edits to it;
// and the scope gate must otherwise ignore it. In-tree state would therefore be
// silently writable by the very agent it constrains. An agent could edit the
// frozen golden copies, delete a key from manifest.json, raise
// max_regress_pct, or — worst — edit the pinned baseline WORKTREE to make the
// BASELINE slow, after which every candidate "improves" and every experiment
// returns KEEP without optimizing anything.
//
// Keyed by a hash of the repository's absolute path so two checkouts of the
// same project never share state.
func StateDir(repoRoot, tag string) (string, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	// Resolve symlinked ancestors (e.g. macOS /tmp -> /private/tmp) so the
	// same repository reached by two different path spellings hashes to the
	// same key. If the path does not exist yet, fall back to the
	// unresolved absolute path rather than failing.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(cache, StateDirName, hex.EncodeToString(sum[:])[:16], tag), nil
}

// Paths within a run's StateDir.
//
// The frozen golden copies and their manifest also live under StateDir, but
// their path constants (StoreDir, ManifestPath) belong to internal/freeze,
// which owns that layout — they are not duplicated here.
const (
	// BaselineFile is the baseline record, relative to StateDir.
	BaselineFile = "baseline.json"
	// WorktreeName is the pinned baseline worktree directory, relative to StateDir.
	WorktreeName = "baseline-worktree"
)

// Baseline records the fixed reference point for a run.
type Baseline struct {
	// Tag is the human-chosen run identifier, e.g. "sep4".
	Tag string `json:"tag"`
	// Branch is the run branch checked out when the baseline was recorded.
	Branch string `json:"branch"`
	// Commit is the short hash the run branch pointed to at baseline time.
	Commit string `json:"commit"`
	// CreatedAt is when the baseline was recorded, in UTC.
	CreatedAt time.Time `json:"created_at"`
	// Benchmarks is the declared benchmark set measured at baseline.
	Benchmarks []string `json:"benchmarks"`
	// Pattern is the -bench regexp derived from Benchmarks.
	Pattern string `json:"pattern"`
	// ConfigSHA256 is the hash of the in-repo config file at baseline time.
	// config.yaml stays in the repository because humans own it and want it in
	// version control, so it is protected by integrity checking rather than by
	// relocation: an agent that raises max_regress_pct or shrinks the
	// benchmark set is caught at eval time when the hash no longer matches.
	ConfigSHA256 string `json:"config_sha256"`
}

// Save writes the baseline as indented JSON, creating parent directories as needed.
func (b *Baseline) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadBaseline reads a baseline written by Save.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no baseline at %s: run 'autoresearch-go baseline' first", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return &b, nil
}

// BenchPattern builds a -bench regexp that matches exactly these benchmarks.
// An empty list yields ".", meaning every benchmark. Each name is escaped
// with regexp.QuoteMeta: names discovered by internal/discover are always
// valid Go identifiers and need no escaping, but benchmarks: in
// config.yaml is documented as hand-editable, and a stray metacharacter in
// a hand-typed name would otherwise silently BROADEN the pattern to match
// benchmarks nobody selected.
func BenchPattern(names []string) string {
	if len(names) == 0 {
		return "."
	}
	var b strings.Builder
	b.WriteString("^(")
	for i, n := range names {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(regexp.QuoteMeta(n))
	}
	b.WriteString(")$")
	return b.String()
}
