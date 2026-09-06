package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Files a run uses to coordinate between the human's shell and the agent's
// loop, relative to StateDir.
//
// Both live out-of-tree alongside the rest of StateDir, for the same reason
// everything else there does (see StateDir): a sentinel inside the
// repository would dirty the working tree the agent commits from, and would
// have to be special-cased in the scope gate and in .gitignore. Out here it
// is invisible to every gate and to git, and `stop` and `eval` still find it
// from either process because StateDir is derived from the repository path
// and run tag, not from anything either process holds privately.
const (
	// StopRequestFile marks that the human has asked the run to end. `eval`
	// reports its presence alongside the verdict; the AGENT decides when to
	// act on it, which is what makes the stop graceful — nothing here
	// interrupts an experiment that is already under way.
	StopRequestFile = "stop.request"

	// EvalPIDFile holds the pid of the currently running `eval`, written on
	// start and removed on exit. It exists so `stop -force` can signal that
	// process rather than guessing, and so `status` can say whether an
	// experiment is in flight.
	EvalPIDFile = "eval.pid"
)

// RequestStop asks the run in stateDir to end after the current experiment.
//
// Creating stateDir if it is missing is deliberate: a human reaching for the
// brake should never be told the directory does not exist yet. A stop
// request against a tag whose baseline never finished is harmless — the
// sentinel simply sits there until a run reads it, or ClearStop removes it.
func RequestStop(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, StopRequestFile)
	if err := os.WriteFile(path, []byte("stop requested\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ClearStop cancels a pending stop request, so the loop may continue.
// Clearing a request that was never made is not an error.
func ClearStop(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, StopRequestFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear stop request: %w", err)
	}
	return nil
}

// StopRequested reports whether a stop is pending for the run in stateDir.
//
// It returns a bool rather than (bool, error) because every caller wants the
// same answer for an unreadable sentinel as for an absent one: carry on. A
// stop that cannot be read must never be allowed to abort a run by itself —
// the human still has -force and Ctrl+C.
func StopRequested(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, StopRequestFile))
	return err == nil
}

// ClaimEval marks an eval as running in stateDir and returns a release
// function to call when it exits.
//
// The claim is an ADVISORY LOCK held on the pid file for the lifetime of the
// process, not merely the file's existence. That distinction is what makes
// `stop -force` safe: an eval killed without cleanup (SIGKILL, a panic, a
// power loss) leaves the file behind, and pids are recycled, so a
// -force that trusted the file alone could eventually signal an unrelated
// process. The lock is released by the kernel when the process dies however
// it dies, so EvalRunning can tell a live eval from a corpse.
//
// Two concurrent evals against the same run is itself a bug — they would
// fight over the same pinned worktree — so a claim that cannot be taken is
// reported as an error naming the incumbent pid rather than silently
// proceeding.
func ClaimEval(stateDir string, pid int) (release func() error, err error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, EvalPIDFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	locked, err := tryLockExclusive(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		other, _, _ := readPIDFile(path)
		f.Close()
		return nil, fmt.Errorf("another autoresearch-go eval (pid %d) is already running for this run", other)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(pid)+"\n"), 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return func() error {
		// Remove before closing: closing drops the lock, and a concurrent
		// EvalRunning that acquired it in between would otherwise read a
		// pid file this process is about to delete. Either order leaves the
		// same end state, and neither can report a live eval that is gone.
		rmErr := os.Remove(path)
		if rmErr != nil && os.IsNotExist(rmErr) {
			rmErr = nil
		}
		if closeErr := f.Close(); rmErr == nil {
			rmErr = closeErr
		}
		return rmErr
	}, nil
}

// EvalRunning reports the pid of the eval currently running for the run in
// stateDir. running is false when no eval holds the claim — including when a
// pid file was left behind by one that died without releasing it.
//
// A pid file that exists but does not hold a plausible pid is an ERROR, not
// a missing one: the value is about to be handed to a kill(2), where a
// negative number means "the whole process group". Refusing to guess is the
// only safe reading of a corrupt file.
func EvalRunning(stateDir string) (pid int, running bool, err error) {
	path := filepath.Join(stateDir, EvalPIDFile)
	pid, present, err := readPIDFile(path)
	if err != nil || !present {
		return 0, false, err
	}
	held, err := claimHeld(path)
	if err != nil {
		return 0, false, err
	}
	return pid, held, nil
}

// ClearEvalPID removes a pid file left behind by an eval that died without
// releasing its claim. Removing one that is not there is not an error.
func ClearEvalPID(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, EvalPIDFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear eval pid: %w", err)
	}
	return nil
}

// readPIDFile parses the pid file at path. present is false when there is no
// file; an unparseable or non-positive pid is an error (see EvalRunning).
func readPIDFile(path string) (pid int, present bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	text := strings.TrimSpace(string(data))
	pid, err = strconv.Atoi(text)
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %q is not a pid", path, text)
	}
	if pid <= 0 {
		return 0, false, fmt.Errorf("read %s: pid %d is not a valid process id", path, pid)
	}
	return pid, true, nil
}

// claimHeld reports whether some live process holds the claim on path. It
// answers by trying to take the lock itself: success means nobody held it,
// so the pid file is a leftover.
func claimHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	locked, err := tryLockExclusive(f)
	if err != nil {
		return false, fmt.Errorf("lock %s: %w", path, err)
	}
	if locked {
		// We took it, so no eval held it. Drop it again immediately: this
		// is a query, not a claim.
		if err := unlock(f); err != nil {
			return false, fmt.Errorf("unlock %s: %w", path, err)
		}
		return false, nil
	}
	return true, nil
}
