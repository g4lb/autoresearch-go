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
func runBaseline(args []string) int {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	tag := fs.String("tag", defaultTag(), "run identifier, e.g. sep4")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*tag) == "" {
		fmt.Fprintln(os.Stderr, "autoresearch-go baseline: -tag must not be empty")
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

	configPath := filepath.Join(root, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		fmt.Fprintln(os.Stderr, "run `autoresearch-go init` first.")
		return exitUsage
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

	if err := gitx.CreateBranch(root, branch); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: create branch %s: %v\n", branch, err)
		return exitUsage
	}

	files, err := discover.TestFiles(root, cfg.Unfreeze)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}
	storeDir := filepath.Join(stateDir, freeze.StoreDir)
	manifest, err := freeze.Snapshot(root, storeDir, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: freeze tests: %v\n", err)
		return exitUsage
	}
	if err := manifest.Save(filepath.Join(stateDir, freeze.ManifestPath)); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: save manifest: %v\n", err)
		return exitUsage
	}

	commit, err := gitx.HeadCommit(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: %v\n", err)
		return exitUsage
	}
	configHash, err := sha256File(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: hash config: %v\n", err)
		return exitUsage
	}

	b := state.Baseline{
		Tag:          *tag,
		Branch:       branch,
		Commit:       commit,
		CreatedAt:    time.Now().UTC(),
		Benchmarks:   cfg.Benchmarks,
		Pattern:      state.BenchPattern(cfg.Benchmarks),
		ConfigSHA256: configHash,
	}
	if err := b.Save(filepath.Join(stateDir, state.BaselineFile)); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: save baseline record: %v\n", err)
		return exitUsage
	}

	// Pin the baseline worktree OUTSIDE the repository, at the exact commit
	// just recorded. Remove any stale worktree left by a previous attempt
	// under the same tag first; RemoveWorktree's error is ignored here
	// because the common case — nothing to remove yet — is itself an error
	// from git, not a distinguishable "already absent" result.
	worktree := filepath.Join(stateDir, state.WorktreeName)
	_ = gitx.RemoveWorktree(root, worktree)
	if err := gitx.AddWorktree(root, worktree, commit); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: pin baseline worktree: %v\n", err)
		return exitUsage
	}

	// Start this run's log fresh: a baseline is a new fixed reference point,
	// so rows measured against a previous baseline no longer apply.
	if err := os.WriteFile(filepath.Join(root, results.Path), []byte(results.Header+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go baseline: init %s: %v\n", results.Path, err)
		return exitUsage
	}

	printBaselineSummary(branch, commit, len(manifest.Files), cfg.Benchmarks)
	return exitOK
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
