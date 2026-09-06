//go:build unix

package main

import (
	"errors"
	"fmt"
	"syscall"
)

// termEval asks the eval at pid to shut down.
//
// SIGTERM, and to the PROCESS rather than to its group, deliberately: eval
// installs a handler that cancels the pipeline context, and cancelling that
// context is what makes internal/runner kill the whole `go test` process
// group including the compiled benchmark binary running as a grandchild.
// Killing eval outright instead would orphan that binary, which then keeps
// burning CPU and corrupts every later measurement on the machine — the
// precise failure -force exists to avoid.
//
// A process that has already exited is not an error: it is the outcome
// wanted.
func termEval(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal eval (pid %d): %w", pid, err)
	}
	return nil
}

// killEvalGroup is the fallback for an eval too wedged to run its own
// cleanup. A negative pid signals the whole process group — see
// internal/runner/procgroup_unix.go, which puts eval's children in one — so
// this reaches the benchmark binary that termEval would have had eval kill
// for itself. It is second, not first, because it costs the leaked-benchmark
// outcome above whenever the polite path would have worked.
func killEvalGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill eval process group (pid %d): %w", pid, err)
	}
	return nil
}
