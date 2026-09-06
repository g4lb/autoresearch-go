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
// cleanup. It signals the whole process group — see
// internal/runner/procgroup_unix.go, which puts eval's children in one — so
// it reaches the benchmark binary that termEval would have had eval kill for
// itself. It is second, not first, because it costs the leaked-benchmark
// outcome above whenever the polite path would have worked.
func killEvalGroup(pid int) error {
	target, err := groupSignalTarget(pid)
	if err != nil {
		return err
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill eval process group (pid %d): %w", pid, err)
	}
	return nil
}

// groupSignalTarget converts a pid into the argument that signals its
// process group: the negated pid.
//
// It refuses pid 1, which is the whole reason this is a function rather than
// a `-pid` at the call site. kill(2) reads -1 not as "process group 1" but
// as EVERY process the caller has permission to signal — so a pid file
// holding "1" would silently turn one wedged experiment into a session-wide
// kill. A pid of 1 is reachable in practice: an `eval` running as a
// container's init has it legitimately, and a corrupt or hand-edited pid
// file can hold anything. Refusing costs nothing — the human still has
// Ctrl+C — while the alternative is unrecoverable.
func groupSignalTarget(pid int) (int, error) {
	if pid <= 1 {
		return 0, fmt.Errorf("refusing to signal the process group of pid %d: "+
			"kill(2) reads a target of -1 as every process this user owns, not as one group. "+
			"Interrupt the eval with Ctrl+C instead", pid)
	}
	return -pid, nil
}
