package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/state"
)

// baselinedRepo returns a repository that has been init'd and baselined
// under tag "sep4" with the run branch checked out — the state every stop
// and status test starts from. origin is the branch that was checked out
// before baseline, for tests that need to step off the run branch.
func baselinedRepo(t *testing.T) (dir, stateDir, origin string) {
	t.Helper()
	dir = copyDemoRepo(t)
	origin, err := gitx.CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}
	stateDir, err = state.StateDir(dir, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	return dir, stateDir, origin
}

func TestStopRequestsAStopForTheCheckedOutRun(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir}) })
	if code != exitOK {
		t.Fatalf("runStop = %d, want %d", code, exitOK)
	}
	if !state.StopRequested(stateDir) {
		t.Fatal("no stop request written")
	}
	if !strings.Contains(stdout, "sep4") {
		t.Errorf("stdout = %q, want it to name the run tag", stdout)
	}
	if !strings.Contains(stdout, "-clear") {
		t.Errorf("stdout = %q, want it to say how to cancel the stop", stdout)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	for i := 0; i < 2; i++ {
		var code int
		captureStdout(t, func() { code = runStop([]string{"-C", dir}) })
		if code != exitOK {
			t.Fatalf("runStop #%d = %d, want %d", i+1, code, exitOK)
		}
	}
	if !state.StopRequested(stateDir) {
		t.Fatal("no stop request after two runStop calls")
	}
}

func TestStopClearCancelsAPendingStop(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	captureStdout(t, func() { runStop([]string{"-C", dir}) })

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir, "-clear"}) })
	if code != exitOK {
		t.Fatalf("runStop -clear = %d, want %d", code, exitOK)
	}
	if state.StopRequested(stateDir) {
		t.Fatal("stop request survived -clear")
	}
	if !strings.Contains(stdout, "sep4") {
		t.Errorf("stdout = %q, want it to name the run tag", stdout)
	}
}

func TestStopOffTheRunBranchPointsAtTag(t *testing.T) {
	dir, _, origin := baselinedRepo(t)
	if err := gitx.Checkout(dir, origin); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr := captureStderr(t, func() { code = runStop([]string{"-C", dir}) })
	if code != exitUsage {
		t.Fatalf("runStop off the run branch = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "-tag") {
		t.Errorf("stderr = %q, want it to point at -tag", stderr)
	}
}

func TestStopAcceptsTagOffTheRunBranch(t *testing.T) {
	// The human's shell may be on any branch; the brake must still work.
	dir, stateDir, origin := baselinedRepo(t)
	if err := gitx.Checkout(dir, origin); err != nil {
		t.Fatal(err)
	}

	var code int
	captureStdout(t, func() { code = runStop([]string{"-C", dir, "-tag", "sep4"}) })
	if code != exitOK {
		t.Fatalf("runStop -tag sep4 = %d, want %d", code, exitOK)
	}
	if !state.StopRequested(stateDir) {
		t.Fatal("no stop request written")
	}
}

func TestStopRejectsATraversalTag(t *testing.T) {
	dir, _, _ := baselinedRepo(t)

	var code int
	stderr := captureStderr(t, func() {
		code = runStop([]string{"-C", dir, "-tag", "../../evil"})
	})
	if code != exitUsage {
		t.Fatalf("runStop -tag ../../evil = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "not allowed") {
		t.Errorf("stderr = %q, want it to reject the tag", stderr)
	}
}

func TestStopForceWithNoRunningEvalStillRequestsTheStop(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir, "-force"}) })
	if code != exitOK {
		t.Fatalf("runStop -force = %d, want %d", code, exitOK)
	}
	if !state.StopRequested(stateDir) {
		t.Fatal("no stop request written")
	}
	if !strings.Contains(stdout, "no eval running") {
		t.Errorf("stdout = %q, want it to say there was nothing to signal", stdout)
	}
}

func TestStopForceReportsTheUnevaluatedCommit(t *testing.T) {
	// After a forced stop the agent's last commit carries no verdict. The
	// human has to be told it is there and how to drop it.
	dir, _, _ := baselinedRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "reuse scratch buffer")

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir, "-force"}) })
	if code != exitOK {
		t.Fatalf("runStop -force = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, "git reset --hard HEAD~1") {
		t.Errorf("stdout = %q, want it to show how to drop the unevaluated commit", stdout)
	}
	if !strings.Contains(stdout, "reuse scratch buffer") {
		t.Errorf("stdout = %q, want it to name the unevaluated commit", stdout)
	}
}

func TestStopForceClearsAStalePIDFile(t *testing.T) {
	// A pid file left by an eval that died without cleanup must not be
	// reported as a running eval, and must not survive the force stop.
	dir, stateDir, _ := baselinedRepo(t)
	pidPath := filepath.Join(stateDir, state.EvalPIDFile)
	if err := os.WriteFile(pidPath, []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir, "-force"}) })
	if code != exitOK {
		t.Fatalf("runStop -force = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, "no eval running") {
		t.Errorf("stdout = %q, want the stale pid file treated as no running eval", stdout)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale pid file survived the force stop")
	}
}
