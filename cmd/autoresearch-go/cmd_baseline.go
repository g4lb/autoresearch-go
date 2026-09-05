package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/discover"
	"github.com/g4lb/autoresearch-go/internal/freeze"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/state"
)

// runBaseline establishes the fixed reference point for one experiment run:
// a fresh run branch, a frozen golden copy of every in-scope test file, a
// pinned detached worktree at the baseline commit, and a baseline record —
// every one of which lives under state.StateDir, OUTSIDE the repository
// being optimized. Only results.tsv, a human-readable log rather than part
// of the metric, stays in the (gitignored) repository.
//
// A single working directory supports one run at a time: the git checkout
// and results.tsv are both repo-scoped, not tag-scoped, so two concurrent
// invocations against the same checkout would race on `git checkout -b` and
// on the results-log guard. Concurrent baseline runs against the same repo
// are unsupported — run them against separate clones instead.
func runBaseline(args []string) int {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	tag := fs.String("tag", defaultTag(), "run identifier, e.g. sep4")
	force := fs.Bool("force", false, "discard an existing results.tsv that already holds experiment rows")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*tag) == "" {
		fmt.Fprintln(os.Stderr, "autoresearch-go baseline: -tag must not be empty")
		return exitUsage
	}
	// Validated here, before state.StateDir or any os.MkdirAll ever sees the
	// raw value: filepath.Join (inside StateDir) resolves ".." components,
	// and MkdirAll would otherwise create a directory wherever a traversal
	// tag lands — long before gitx.CreateBranch's own ref-name rules would
	// get a chance to reject it. state.StateDir enforces the same check
	// internally too, but failing fast here means a bad -tag never reaches
	// any filesystem call at all, not even the state directory lookup.
	if err := state.ValidTag(*tag); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}

	root, err := gitx.Root(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %s is not inside a git repository: %v\n", *dir, err)
		return exitUsage
	}

	// Resolve the out-of-tree state directory immediately: every artifact
	// below except results.tsv is written there, never into root.
	stateDir, err := state.StateDir(root, *tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}

	cfg, configPath, code := loadConfig("baseline", root)
	if code != exitOK {
		return code
	}

	// A dirty tree means the commit baseline pins would not reflect what is
	// actually on disk: the baseline would not be reproducible.
	clean, err := gitx.IsClean(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}
	if !clean {
		fmt.Fprintln(os.Stderr, "autoresearch-go baseline: working tree is dirty; a baseline recorded "+
			"against uncommitted changes is not reproducible. Commit or stash your changes first.")
		return exitUsage
	}

	// A reused tag would silently compare later candidates against whatever
	// commit the old branch happens to point to now, not a fresh baseline.
	branch := "autoresearch-go/" + *tag
	exists, err := gitx.BranchExists(root, branch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}
	if exists {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: branch %s already exists; the branch must be "+
			"fresh — a reused tag would compare against the wrong commit. Pick a different -tag.\n", branch)
		return exitUsage
	}

	// A configured benchmark that does not exist would pin the run to
	// something no `eval` can ever measure: nothing catches it until
	// measure.Run fails with "no benchmarks matched", by which point the
	// human has walked away for the night. Catch it here, before the branch
	// exists, so a config typo never needs rolling back. An empty
	// Benchmarks list means "all discovered" and stays valid.
	if len(cfg.Benchmarks) > 0 {
		discovered, err := discover.Benchmarks(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
			return exitUsage
		}
		available := discover.BaseNames(discovered)
		known := make(map[string]bool, len(available))
		for _, n := range available {
			known[n] = true
		}
		var unknown []string
		for _, n := range cfg.Benchmarks {
			if !known[n] {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %s names unknown benchmark(s): %s\n",
				configPath, strings.Join(unknown, ", "))
			fmt.Fprintf(os.Stderr, "available: %s\n", strings.Join(available, ", "))
			return exitUsage
		}
	}

	// results.tsv is the sole, durable record of a previous unattended run.
	// A fresh baseline must not silently truncate it: check — and refuse,
	// absent -force — before anything else is created or mutated, so a
	// refusal here leaves no half-made branch, freeze, or worktree behind.
	resultsPath := filepath.Join(root, results.Path)
	existingRows, err := results.Load(resultsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %s: %v\n", resultsPath, err)
		return exitUsage
	}
	if len(existingRows) > 0 && !*force {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %s already holds %d experiment row(s) from a "+
			"previous run; a fresh baseline would erase that record with no way to get it back.\n", resultsPath, len(existingRows))
		fmt.Fprintln(os.Stderr, "Move or delete it if you no longer need it, or re-run with -force to discard it.")
		return exitUsage
	}

	// Recorded before CreateBranch so a failure anywhere below can put the
	// working tree back exactly where it found it.
	originalBranch, err := gitx.CurrentBranch(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}

	if err := gitx.CreateBranch(root, branch); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: create branch %s: %v\n", branch, err)
		return exitUsage
	}

	// Everything from here on can fail partway through (full disk,
	// permissions, a stale lock). If it does, the run branch created above
	// must not be left behind consuming this -tag permanently: roll back to
	// originalBranch and delete branch before reporting the failure.
	commit, frozenCount, err := finishBaseline(root, stateDir, configPath, resultsPath, branch, *tag, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		if coErr := gitx.Checkout(root, originalBranch); coErr != nil {
			fmt.Fprintf(os.Stderr, "autoresearch-go baseline: cleanup did not fully succeed: checkout %s: %v\n"+
				"you may need to run `git checkout %s && git branch -D %s` by hand.\n",
				originalBranch, coErr, originalBranch, branch)
			return exitUsage
		}
		if dErr := gitx.DeleteBranch(root, branch); dErr != nil {
			fmt.Fprintf(os.Stderr, "autoresearch-go baseline: cleanup did not fully succeed: delete branch %s: %v\n"+
				"you may need to run `git branch -D %s` by hand.\n", branch, dErr, branch)
		}
		return exitUsage
	}

	printBaselineSummary(branch, commit, frozenCount, cfg.Benchmarks)
	return exitOK
}

// finishBaseline performs every remaining baseline step once the run branch
// exists: freezing in-scope test files, recording the baseline, pinning the
// worktree, and resetting results.tsv. Split out from runBaseline so a
// failure at any point here can be compensated for uniformly by its caller
// (checking the original branch back out and deleting the run branch)
// instead of duplicating that rollback at every return site.
func finishBaseline(root, stateDir, configPath, resultsPath, branch, tag string, cfg config.Config) (commit string, frozenCount int, err error) {
	files, err := discover.TestFiles(root, cfg.Unfreeze)
	if err != nil {
		return "", 0, err
	}
	storeDir := filepath.Join(stateDir, freeze.StoreDir)
	manifest, err := freeze.Snapshot(root, storeDir, files)
	if err != nil {
		return "", 0, fmt.Errorf("freeze tests: %w", err)
	}
	if err := manifest.Save(filepath.Join(stateDir, freeze.ManifestPath)); err != nil {
		return "", 0, fmt.Errorf("save manifest: %w", err)
	}

	commit, err = gitx.HeadCommit(root)
	if err != nil {
		return "", 0, err
	}
	configHash, err := sha256File(configPath)
	if err != nil {
		return "", 0, fmt.Errorf("hash config: %w", err)
	}

	b := state.Baseline{
		Tag:    tag,
		Branch: branch,
		Commit: commit,
		// MeasureCommit starts equal to the frozen anchor; internal/pipeline
		// advances it after every KEEP. See state.Baseline's doc comment.
		MeasureCommit: commit,
		CreatedAt:     time.Now().UTC(),
		Benchmarks:    cfg.Benchmarks,
		Pattern:       state.BenchPattern(cfg.Benchmarks),
		ConfigSHA256:  configHash,
	}
	if err := b.Save(filepath.Join(stateDir, state.BaselineFile)); err != nil {
		return "", 0, fmt.Errorf("save baseline record: %w", err)
	}

	// Pin the baseline worktree OUTSIDE the repository, at the exact commit
	// just recorded. Remove any stale worktree left by a previous attempt
	// under the same tag first; RemoveWorktree's error is ignored here
	// because the common case — nothing to remove yet — is itself an error
	// from git, not a distinguishable "already absent" result.
	worktree := filepath.Join(stateDir, state.WorktreeName)
	_ = gitx.RemoveWorktree(root, worktree)
	if err := gitx.AddWorktree(root, worktree, commit); err != nil {
		return "", 0, fmt.Errorf("pin baseline worktree: %w", err)
	}

	// Start this run's log fresh: a baseline is a new fixed reference point,
	// so rows measured against a previous baseline no longer apply. The
	// caller's guard already established this is safe: resultsPath is either
	// missing, header-only, or -force was passed.
	if err := os.WriteFile(resultsPath, []byte(results.Header+"\n"), 0o644); err != nil {
		return "", 0, fmt.Errorf("init %s: %w", results.Path, err)
	}

	return commit, len(manifest.Files), nil
}

// sha256File hashes a file's contents, hex-encoded.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// printBaselineSummary reports what baseline pinned and the next command.
func printBaselineSummary(branch, commit string, frozenCount int, benchmarks []string) {
	fmt.Println("autoresearch-go baseline: recorded a fresh baseline")
	fmt.Printf("  branch:     %s\n", branch)
	fmt.Printf("  commit:     %s\n", commit)
	fmt.Printf("  frozen:     %d test file(s)\n", frozenCount)
	fmt.Printf("  benchmarks: %s\n", strings.Join(benchmarks, ", "))
	fmt.Println("\nnext:")
	fmt.Println("  autoresearch-go eval")
}
