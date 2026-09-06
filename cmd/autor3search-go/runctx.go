package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/state"
)

// runRef is everything a command needs to address ONE run: where the
// repository is, which run it belongs to, and where that run's out-of-tree
// state lives. It stops short of loading the baseline record, so `stop` can
// brake a run whose `baseline` never finished.
type runRef struct {
	Root     string
	Branch   string
	Tag      string
	StateDir string
}

// WorktreeDir is the pinned baseline worktree for this run. Its presence is
// not guaranteed — `baseline` writes baseline.json before pinning it, so an
// interrupted baseline leaves the record without the worktree.
func (r runRef) WorktreeDir() string { return filepath.Join(r.StateDir, state.WorktreeName) }

// resolveRunRef locates the run a command should act on.
//
// The tag normally comes from the checked-out branch rather than a flag, so
// there is no way to point `eval` at the wrong run by mistake. tagOverride
// relaxes that for the commands a HUMAN runs from their own shell (`status`,
// `stop`), which must keep working when that shell is on another branch —
// a brake that only works from the right branch is not a brake. It is never
// offered to `eval`.
//
// On failure the message is already on stderr and the returned code is the
// one the command should exit with.
func resolveRunRef(cmdName, dir, tagOverride string) (runRef, int) {
	root, err := gitx.Root(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go %s: %s is not inside a git repository: %v\n", cmdName, dir, err)
		return runRef{}, exitUsage
	}
	branch, err := gitx.CurrentBranch(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go %s: %v\n", cmdName, err)
		return runRef{}, exitUsage
	}

	tag := tagOverride
	if tag == "" {
		if !strings.HasPrefix(branch, branchPrefix) {
			fmt.Fprintf(os.Stderr, "autor3search-go %s: current branch %q is not a run branch "+
				"(expected %s<tag>). Check out your run branch first, e.g. "+
				"`git checkout %ssep4`, name the run with -tag, or run "+
				"`autor3search-go baseline` if you have not started a run yet.\n",
				cmdName, branch, branchPrefix, branchPrefix)
			return runRef{}, exitUsage
		}
		tag = strings.TrimPrefix(branch, branchPrefix)
	}

	// A tag taken from a branch name has already passed git's own ref rules,
	// but a -tag flag has passed nothing. Both are validated here: the value
	// is about to be joined into an out-of-tree path that os.MkdirAll will
	// create, long before any git operation could reject it.
	if err := state.ValidTag(tag); err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go %s: %v\n", cmdName, err)
		return runRef{}, exitUsage
	}
	stateDir, err := state.StateDir(root, tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go %s: %v\n", cmdName, err)
		return runRef{}, exitUsage
	}
	return runRef{Root: root, Branch: branch, Tag: tag, StateDir: stateDir}, exitOK
}
