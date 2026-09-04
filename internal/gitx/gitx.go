// Package gitx wraps the git commands autoresearch-go needs.
package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
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
func ChangedSince(dir, commit string) ([]string, error) {
	tracked, err := git(dir, "diff", "--name-only", commit)
	if err != nil {
		return nil, err
	}
	untracked, err := git(dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, block := range []string{tracked, untracked} {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
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
func RemoveWorktree(repoDir, path string) error {
	_, err := git(repoDir, "worktree", "remove", "--force", path)
	return err
}
