//go:build unix

package state_test

import (
	"testing"

	"github.com/g4lb/autoresearch-go/internal/state"
)

// Unix-only: the assertion is that a HELD claim reads as running, which is
// what the flock in evallock_unix.go provides. Elsewhere tryLockExclusive
// is a no-op and EvalRunning falls back to the pid file's existence.
func TestClaimEvalIsVisibleToEvalRunning(t *testing.T) {
	dir := t.TempDir()
	release, err := state.ClaimEval(dir, 4321)
	if err != nil {
		t.Fatalf("ClaimEval: %v", err)
	}
	defer release()

	pid, running, err := state.EvalRunning(dir)
	if err != nil {
		t.Fatalf("EvalRunning: %v", err)
	}
	if !running || pid != 4321 {
		t.Fatalf("EvalRunning = (%d, %v); want (4321, true)", pid, running)
	}
}
