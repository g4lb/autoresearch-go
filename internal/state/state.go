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
const StateDirName = "autor3search-go"

// StateHomeEnv names the environment variable that relocates every run's
// out-of-tree state, replacing the default location under the user cache.
//
// It exists for the two cases where the user cache is the wrong place: a
// container or CI runner with no durable cache to speak of, and a TEST
// SUITE, which would otherwise accumulate a directory per temporary
// repository in the developer's real cache forever — this project's own
// tests create one on every run.
//
// The value must be an absolute path, and run state is keyed underneath it
// exactly as it is under the cache: one directory per repository, one per
// tag within that.
const StateHomeEnv = "AUTOR3SEARCH_GO_STATE_HOME"

// validTagPattern is the strict allow-list ValidTag enforces: letters,
// digits, '.', '_' and '-'. Notably absent is '/' (or any other path
// separator), which alone is enough to block both directory traversal
// ("../../etc") and an absolute path ("/etc/passwd") — a tag can never
// contain more than one path segment.
var validTagPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidTag reports whether tag is safe to use as a filesystem path segment.
//
// StateDir joins tag directly into an out-of-tree path with filepath.Join,
// which resolves ".." components, and callers create that path with
// os.MkdirAll immediately afterward — long before git's own ref-name rules
// (e.g. inside gitx.CreateBranch) would ever get a chance to reject a bad
// tag. Without this check, a tag like "../../../../tmp/evil" reaches
// MkdirAll and creates a directory wherever the traversal lands, before any
// git operation runs at all.
//
// ValidTag closes that gap with a strict allow-list rather than trying to
// enumerate dangerous sequences: only letters, digits, '.', '_' and '-' are
// permitted, and "." and ".." are rejected even though both characters are
// individually allowed, since either one alone means "this directory" or
// "the parent directory" rather than naming anything.
func ValidTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag must not be empty")
	}
	if tag == "." || tag == ".." {
		return fmt.Errorf("tag %q is not allowed: %q is a directory reference, not a run identifier", tag, tag)
	}
	if !validTagPattern.MatchString(tag) {
		return fmt.Errorf("tag %q is not allowed: tags may contain only letters, digits, '.', '_' and '-'", tag)
	}
	return nil
}

// StateDir returns the OUT-OF-TREE directory holding every piece of state the
// metric depends on, for one repository and run tag.
//
// NONE of this may live inside the repository. The agent edits the repository;
// `.autor3search/` is gitignored so ChangedSince cannot observe edits to it;
// and the scope gate must otherwise ignore it. In-tree state would therefore be
// silently writable by the very agent it constrains. An agent could edit the
// frozen golden copies, delete a key from manifest.json, raise
// max_regress_pct, or — worst — edit the pinned baseline WORKTREE to make the
// BASELINE slow, after which every candidate "improves" and every experiment
// returns KEEP without optimizing anything.
//
// Keyed by a hash of the repository's absolute path so two checkouts of the
// same project never share state. tag is validated with ValidTag before it
// ever reaches a filesystem path, so every caller of StateDir — not just
// `baseline` — gets the same guarantee against a traversal tag.
func StateDir(repoRoot, tag string) (string, error) {
	if err := ValidTag(tag); err != nil {
		return "", err
	}
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
	home, err := stateHome()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(home, hex.EncodeToString(sum[:])[:16], tag), nil
}

// stateHome returns the directory holding every repository's run state:
// StateHomeEnv when set, otherwise the conventional spot under the user
// cache.
//
// A relative override is refused rather than resolved. filepath.Join would
// happily accept one, but the result would then depend on the working
// directory each command was invoked from — so `eval` run from a
// subdirectory and `stop` run from the repository root would address
// DIFFERENT state for the same run, and the brake would silently miss.
func stateHome() (string, error) {
	if home := os.Getenv(StateHomeEnv); home != "" {
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("%s must be an absolute path, got %q: a relative state home would "+
				"resolve differently depending on where each command is run from", StateHomeEnv, home)
		}
		return home, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(cache, StateDirName), nil
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

// Baseline records the reference point(s) for a run.
//
// Two distinct reference points are tracked here, deliberately kept apart:
//
//   - Commit is the FROZEN anchor: the commit the run started from. It is
//     recorded once by `baseline` and must NEVER change for the life of the
//     run. The frozen test snapshots are taken relative to it, and the scope
//     gate's ChangedSince diffs against it (see internal/pipeline) — using a
//     fixed anchor there means the scope gate keeps re-validating the FULL
//     accumulated diff against the human-approved starting point on every
//     single eval, rather than trusting that anything the agent already
//     banked as a KEEP must have been in scope. An agent that could move
//     this anchor (or a bug that let it drift) could launder an
//     out-of-scope edit into the "already accepted" state after only one
//     eval, permanently invisible to the scope gate from then on.
//   - MeasureCommit is the ADVANCING measurement pointer: the commit the
//     pinned baseline worktree is actually checked out to, and what every
//     `eval` measures the candidate against. It starts equal to Commit and
//     is re-pointed to the candidate's own commit after every KEEP (see
//     internal/pipeline), so each `eval` answers "did THIS change help",
//     not "is the tree better than when the run started" — the latter is
//     what let a no-op experiment coast to KEEP on the strength of an
//     earlier, already-banked improvement.
//
// Collapsing these two into one field would either freeze the measurement
// baseline forever (letting stale early wins mask later no-ops as KEEP) or
// let the scope gate's comparison point drift (letting an agent accumulate
// out-of-scope edits across kept experiments without ever being re-checked
// against the true starting point). Keep them separate.
type Baseline struct {
	// Tag is the human-chosen run identifier, e.g. "sep4".
	Tag string `json:"tag"`
	// Branch is the run branch checked out when the baseline was recorded.
	Branch string `json:"branch"`
	// Commit is the short hash the run branch pointed to at baseline time —
	// the FROZEN anchor. See the type doc comment: this must never change
	// after `baseline` records it.
	Commit string `json:"commit"`
	// MeasureCommit is the short hash currently checked out in the pinned
	// baseline worktree — the ADVANCING measurement anchor `eval` compares
	// each candidate against. It starts equal to Commit and moves to the
	// candidate's commit after every KEEP. Unlike Commit, it is expected to
	// change over the life of a run. See the type doc comment for why the
	// two are kept separate.
	MeasureCommit string `json:"measure_commit"`
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
		return nil, fmt.Errorf("no baseline at %s: run 'autor3search-go baseline' first", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	// A baseline.json written before MeasureCommit existed has no
	// "measure_commit" field, which unmarshals to "". Fall back to Commit —
	// exactly the value a fresh baseline would have started MeasureCommit
	// at — rather than leaving it empty, which would fail the worktree
	// integrity check in internal/pipeline on the very first eval of an
	// old run.
	if b.MeasureCommit == "" {
		b.MeasureCommit = b.Commit
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
