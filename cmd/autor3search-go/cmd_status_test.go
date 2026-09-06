package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/results"
	"github.com/g4lb/autor3search-go/internal/state"
)

func TestStatusShowsTheRunContext(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)

	var code int
	stdout := captureStdout(t, func() { code = runStatus([]string{"-C", dir}) })
	if code != exitOK {
		t.Fatalf("runStatus = %d, want %d", code, exitOK)
	}
	for _, want := range []string{
		"sep4",                // the run tag
		branchPrefix + "sep4", // the branch the agent commits on
		filepath.Join(stateDir, state.WorktreeName), // the pinned worktree
		"autor3search-go stop",                      // how to stop
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
}

func TestStatusReportsNoExperimentsYet(t *testing.T) {
	dir, _, _ := baselinedRepo(t)

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	if !strings.Contains(stdout, "none yet") {
		t.Errorf("status output = %q, want it to say no experiments have run", stdout)
	}
}

func TestStatusCountsExperimentsByStatus(t *testing.T) {
	dir, _, _ := baselinedRepo(t)
	path := filepath.Join(dir, results.Path)
	for _, r := range []results.Row{
		{Commit: "aaa1111", Score: 0.91, Status: "keep", Description: "prealloc-map"},
		{Commit: "bbb2222", Score: 0.99, Status: "discard", Description: "byte-scan"},
		{Commit: "ccc3333", Score: 1.00, Status: "fail", Description: "edited-test"},
	} {
		if err := results.Append(path, r); err != nil {
			t.Fatal(err)
		}
	}

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	for _, want := range []string{"3 run", "1 keep", "1 discard", "1 fail"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
}

func TestStatusReportsAPendingStop(t *testing.T) {
	dir, _, _ := baselinedRepo(t)
	captureStdout(t, func() { runStop([]string{"-C", dir}) })

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	if !strings.Contains(stdout, "requested") {
		t.Errorf("status output = %q, want it to report the pending stop", stdout)
	}
	if strings.Contains(stdout, "not requested") {
		t.Errorf("status output = %q, reported the pending stop as absent", stdout)
	}
}

func TestStatusReportsNoPendingStop(t *testing.T) {
	dir, _, _ := baselinedRepo(t)

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	if !strings.Contains(stdout, "not requested") {
		t.Errorf("status output = %q, want it to report no pending stop", stdout)
	}
}

func TestStatusFlagsThatTheRunBranchIsNotCheckedOut(t *testing.T) {
	// Asked about a run from another branch, status must not imply the
	// agent is committing where the human is standing.
	dir, _, origin := baselinedRepo(t)
	if err := gitx.Checkout(dir, origin); err != nil {
		t.Fatal(err)
	}

	var code int
	stdout := captureStdout(t, func() { code = runStatus([]string{"-C", dir, "-tag", "sep4"}) })
	if code != exitOK {
		t.Fatalf("runStatus -tag sep4 = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, "NOT checked out") {
		t.Errorf("status output = %q, want it to flag that the run branch is not checked out", stdout)
	}
}

func TestStatusReportsAMissingWorktree(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	if err := gitx.RemoveWorktree(dir, filepath.Join(stateDir, state.WorktreeName)); err != nil {
		t.Fatal(err)
	}

	var code int
	stdout := captureStdout(t, func() { code = runStatus([]string{"-C", dir}) })
	if code != exitOK {
		t.Fatalf("runStatus with no worktree = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, "MISSING") {
		t.Errorf("status output = %q, want it to flag the missing worktree", stdout)
	}
}

func TestStatusReportsAnIdleLoop(t *testing.T) {
	dir, _, _ := baselinedRepo(t)

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	if !strings.Contains(stdout, "idle") {
		t.Errorf("status output = %q, want it to say no experiment is in flight", stdout)
	}
}

func TestStatusWithoutABaselineSaysSo(t *testing.T) {
	// An honest answer for a repo where nobody has started a run beats the
	// raw "no baseline at <cache path>" error.
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	var code int
	stderr := captureStderr(t, func() { code = runStatus([]string{"-C", dir, "-tag", "sep4"}) })
	if code != exitUsage {
		t.Fatalf("runStatus with no baseline = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "baseline") {
		t.Errorf("stderr = %q, want it to point at baseline", stderr)
	}
}

func TestStatusReportsARunningEval(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	release, err := state.ClaimEval(stateDir, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	stdout := captureStdout(t, func() { runStatus([]string{"-C", dir}) })
	if !strings.Contains(stdout, "running") {
		t.Errorf("status output = %q, want it to report the eval in flight", stdout)
	}
}
