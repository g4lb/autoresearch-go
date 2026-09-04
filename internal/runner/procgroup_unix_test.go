//go:build unix

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupTimeoutOnUnix(t *testing.T) {
	// Verify that process groups kill the entire child process tree on timeout.
	// go test execs the test binary as a grandchild of the go command.
	// Without process group setup, a timeout kills only the direct child (go),
	// leaving the grandchild test binary orphaned and consuming CPU.
	// With process groups, both are killed together.
	//
	// This test records the PID of the test binary, triggers a timeout,
	// and verifies the binary is killed by checking process existence.

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "pid")

	// Create a module whose test writes its PID then sleeps 60s
	modDir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(modDir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module orphan\n\ngo 1.21\n")
	write("orphan_test.go", `package orphan

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestSleeper(t *testing.T) {
	if p := os.Getenv("ORPHAN_PID_FILE"); p != "" {
		os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	time.Sleep(60 * time.Second)
}
`)

	// Run test with 8s timeout. This is long enough for:
	// - Module compilation (1-2s)
	// - Test binary startup and PID recording (< 1s)
	// - Timeout trigger (< 8s)
	// If timeout were milliseconds, go would be killed before the test binary
	// ever execs, and there would be no grandchild to orphan. This is why
	// the critical defect was subtle: short timeouts still passed on luck.
	r := New(modDir, 8*time.Second, nil)
	r.Env = append(os.Environ(), "ORPHAN_PID_FILE="+pidFile)

	res, err := r.Test(context.Background(), false)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, want true (exit=%d)", res.ExitCode)
	}

	// Read the PID file. If it doesn't exist or is empty, the binary never
	// started before timeout, so there's nothing to assert about process groups.
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("test binary never wrote PID file (compilation or startup too slow)")
		}
		t.Fatalf("ReadFile: %v", err)
	}

	// Parse the PID
	var pid int
	if len(pidBytes) > 0 {
		var i int64
		for _, b := range pidBytes {
			if b >= '0' && b <= '9' {
				i = i*10 + int64(b-'0')
			}
		}
		pid = int(i)
	}

	if pid == 0 {
		t.Skip("could not parse test binary PID from file")
	}

	// Clean up defensively: if the test fails, kill the 60s sleeper.
	// The test should pass and the process should already be gone.
	defer func() {
		if pid != 0 {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}()

	// Poll for the process to be gone. The test binary was killed when its
	// process group was signaled on timeout. Poll to avoid race conditions.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			// Process gone: killed with its process group on timeout
			pid = 0 // Don't kill it again in defer
			return
		}
		if err != nil && err != syscall.ESRCH {
			// Some other error (EPERM, etc.)
			t.Errorf("Kill(pid, 0): %v", err)
			return
		}

		// Process still exists
		if time.Now().After(deadline) {
			t.Fatalf("test binary PID %d still alive 5s after timeout; process group was not killed", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
