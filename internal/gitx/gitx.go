// Package gitx wraps the git commands autoresearch-go needs.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// git runs a git subcommand and returns its trimmed stdout. Stdout and
// stderr are captured separately so that stderr chatter on an otherwise
// successful command — git-lfs smudge/filter warnings, advice.* hints,
// locale warnings, or a user's custom hooks writing to stderr — never gets
// parsed as part of the result. Stderr is included in the error message
// only when the command fails.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Root returns the repository root containing dir.
func Root(dir string) (string, error) { return git(dir, "rev-parse", "--show-toplevel") }

// HeadCommit returns the short hash of HEAD.
func HeadCommit(dir string) (string, error) {
	return git(dir, "rev-parse", "--short=7", "HEAD")
}

// CurrentBranch returns the checked-out branch name.
func CurrentBranch(dir string) (string, error) {
	return git(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// BranchExists reports whether a local branch exists.
func BranchExists(dir, name string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil
		}
		return false, fmt.Errorf("check branch %s: %w", name, err)
	}
	return true, nil
}

// CreateBranch creates and checks out a branch at HEAD.
func CreateBranch(dir, name string) error {
	_, err := git(dir, "checkout", "-b", name)
	return err
}

// Checkout switches the working tree to an existing branch (or other ref).
// Used to compensate for a run branch created but not completed: on a later
// failure the caller checks the original branch back out before deleting
// the abandoned one with DeleteBranch.
func Checkout(dir, ref string) error {
	_, err := git(dir, "checkout", ref)
	return err
}

// DeleteBranch force-deletes a local branch. Used to undo a run branch left
// behind by a baseline (or similar) attempt that failed partway through, so
// a retry under the same name is not permanently blocked.
func DeleteBranch(dir, name string) error {
	_, err := git(dir, "branch", "-D", name)
	return err
}

// IsClean reports whether the working tree has no changes.
func IsClean(dir string) (bool, error) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// ChangedSince lists repo-relative paths modified since commit, including
// files that are still untracked.
//
// Both underlying git calls use -z (NUL-separated output) instead of the
// default newline-separated form. Without -z, git quotes and octal-escapes
// any path containing non-ASCII bytes, quotes, or backslashes (for example
// "internal/caf\303\251.go"), which would then fail scope matching. -z
// output is not newline-terminated, so it is split on NUL explicitly
// rather than reusing the newline-splitting logic.
func ChangedSince(dir, commit string) ([]string, error) {
	tracked, err := git(dir, "diff", "--name-only", "-z", commit)
	if err != nil {
		return nil, err
	}
	untracked, err := git(dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, block := range []string{tracked, untracked} {
		for _, entry := range strings.Split(block, "\x00") {
			if entry == "" || seen[entry] {
				continue
			}
			seen[entry] = true
			out = append(out, entry)
		}
	}
	sort.Strings(out)
	return out, nil
}

// AddWorktree checks commit out into a detached worktree at path.
func AddWorktree(repoDir, path, commit string) error {
	_, err := git(repoDir, "worktree", "add", "--detach", path, commit)
	return err
}

// RemoveWorktree deletes a worktree previously created by AddWorktree.
//
// It passes --force, which silently discards any uncommitted or untracked
// changes present in the target worktree, with no diagnostic. git still
// refuses to remove the repository's main working tree or a path that is
// not a registered worktree, but callers must only ever point this at a
// worktree the harness itself created (e.g. the baseline checkout from
// AddWorktree) — never at a path a human or the agent might have live,
// unsaved work in.
func RemoveWorktree(repoDir, path string) error {
	_, err := git(repoDir, "worktree", "remove", "--force", path)
	return err
}
