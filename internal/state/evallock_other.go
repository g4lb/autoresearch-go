//go:build !unix

package state

import "os"

// tryLockExclusive has no advisory-locking equivalent here, so it reports
// the lock as always available. The consequence is that EvalRunning falls
// back to trusting the pid file's existence, and a pid file left behind by
// a killed eval reads as "no eval running" — the conservative direction:
// `stop -force` is unsupported on this platform anyway (see the command's
// platform-specific implementation), so nothing here signals a pid.
func tryLockExclusive(f *os.File) (bool, error) { return true, nil }

// unlock is the matching no-op.
func unlock(f *os.File) error { return nil }
