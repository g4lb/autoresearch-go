//go:build unix

package state

import (
	"errors"
	"os"
	"syscall"
)

// tryLockExclusive takes a non-blocking exclusive advisory lock on f,
// reporting false when another process already holds it.
//
// flock(2) is used rather than a lockfile-plus-pid convention because the
// kernel releases it when the holding process dies by ANY means, including a
// SIGKILL that runs no cleanup. That is exactly the case a stop-and-kill
// path has to get right — see ClaimEval.
func tryLockExclusive(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

// unlock releases a lock taken by tryLockExclusive.
func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
