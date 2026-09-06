//go:build unix

package main

import (
	"bufio"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/g4lb/autoresearch-go/internal/state"
)

// The stand-in eval runs as a re-exec of this test binary, so it holds the
// run's claim in a REAL other process. That matters: the claim is an flock
// the kernel releases when its holder dies, and `stop -force` waits on that
// release rather than on the pid (see waitForRelease). Claiming from inside
// the test process would keep the lock held after the stand-in died and test
// nothing.
const (
	helperEnv       = "AUTORESEARCH_STOP_HELPER_STATEDIR"
	helperIgnoreEnv = "AUTORESEARCH_STOP_HELPER_IGNORE_TERM"
)

// TestStopForceStandInEval is not a test. It is the child process the two
// tests below re-exec: it claims the run, announces itself, and waits to be
// signalled.
func TestStopForceStandInEval(t *testing.T) {
	stateDir := os.Getenv(helperEnv)
	if stateDir == "" {
		t.Skip("not the stand-in eval child process")
	}
	if os.Getenv(helperIgnoreEnv) == "1" {
		// A wedged eval: takes SIGTERM and carries on regardless.
		signal.Ignore(syscall.SIGTERM)
	}
	release, err := state.ClaimEval(stateDir, os.Getpid())
	if err != nil {
		t.Fatalf("stand-in ClaimEval: %v", err)
	}
	defer release()

	// Tell the parent the claim is held before it reaches for the brake.
	os.Stdout.WriteString("claimed\n")
	time.Sleep(60 * time.Second)
}

// startStandInEval re-execs this test binary as a process holding the run's
// claim, in its own process group the way internal/runner starts eval's
// children. It returns once the child has actually claimed the run.
func startStandInEval(t *testing.T, stateDir string, ignoreTerm bool) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestStopForceStandInEval", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), helperEnv+"="+stateDir)
	if ignoreTerm {
		cmd.Env = append(cmd.Env, helperIgnoreEnv+"=1")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stand-in eval: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})

	ready := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			if strings.TrimSpace(s.Text()) == "claimed" {
				ready <- "claimed"
				return
			}
		}
		ready <- "child exited without claiming the run"
	}()
	select {
	case msg := <-ready:
		if msg != "claimed" {
			t.Fatal(msg)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stand-in eval never claimed the run")
	}
	return cmd
}

// TestStopForceTerminatesARunningEval is the point of -force: a real process
// holding the run's claim is actually gone afterwards.
func TestStopForceTerminatesARunningEval(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	cmd := startStandInEval(t, stateDir, false)

	var code int
	stdout := captureStdout(t, func() { code = runStop([]string{"-C", dir, "-force"}) })
	if code != exitOK {
		t.Fatalf("runStop -force = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, "signalling eval") {
		t.Errorf("stdout = %q, want it to report signalling the running eval", stdout)
	}
	if strings.Contains(stdout, "killing its process group") {
		t.Errorf("stdout = %q, want SIGTERM alone to have been enough", stdout)
	}
	if !state.StopRequested(stateDir) {
		t.Error("no stop request written by -force")
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("wait for stand-in eval: %v", err)
	}
	if _, running, _ := state.EvalRunning(stateDir); running {
		t.Error("the run is still claimed after stop -force")
	}
}

// TestStopForceEscalatesToTheProcessGroup covers the wedged case: an eval
// that ignores SIGTERM must still be stopped, because a human reaching for
// -force cannot be left with a benchmark burning CPU.
func TestStopForceEscalatesToTheProcessGroup(t *testing.T) {
	dir, stateDir, _ := baselinedRepo(t)
	cmd := startStandInEval(t, stateDir, true)

	var (
		code   int
		stdout string
	)
	done := make(chan struct{})
	go func() {
		stdout = captureStdout(t, func() {
			code = runStop([]string{"-C", dir, "-force", "-grace", "500ms"})
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("runStop -force hung on an eval that ignores SIGTERM")
	}

	if code != exitOK {
		t.Fatalf("runStop -force on a wedged eval = %d, want %d\n%s", code, exitOK, stdout)
	}
	if !strings.Contains(stdout, "killing its process group") {
		t.Errorf("stdout = %q, want it to report escalating past SIGTERM", stdout)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("wait for wedged stand-in eval: %v", err)
	}
	if _, running, _ := state.EvalRunning(stateDir); running {
		t.Error("the wedged eval still claims the run after stop -force")
	}
	// A SIGKILLed eval runs no cleanup of its own, so -force has to remove
	// the pid file it left behind.
	if _, err := os.Stat(filepath.Join(stateDir, state.EvalPIDFile)); !os.IsNotExist(err) {
		t.Error("pid file survived a killed eval")
	}
}
