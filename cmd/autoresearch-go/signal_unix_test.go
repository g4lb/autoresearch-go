//go:build unix

package main

import "testing"

// TestGroupSignalTargetRefusesPID1 is the guard on kill(2)'s most dangerous
// argument. killEvalGroup negates the pid to signal a whole process group,
// and pid 1 negates to -1 — which POSIX defines not as "process group 1" but
// as EVERY process the caller has permission to signal. A pid file holding
// "1" (corrupt, hand-edited, or an eval genuinely running as a container's
// init) would otherwise turn `stop -force` into a session-wide kill.
//
// The check is a pure function precisely so this test can exercise it
// without a syscall: a test that called killEvalGroup(1) against a missing
// guard would take the developer's machine down with it.
func TestGroupSignalTargetRefusesPID1(t *testing.T) {
	if _, err := groupSignalTarget(1); err == nil {
		t.Fatal("groupSignalTarget(1) returned no error; kill(-1) signals every process the user owns")
	}
}

func TestGroupSignalTargetRefusesNonPositivePIDs(t *testing.T) {
	for _, pid := range []int{0, -1, -42} {
		if _, err := groupSignalTarget(pid); err == nil {
			t.Errorf("groupSignalTarget(%d) returned no error", pid)
		}
	}
}

func TestGroupSignalTargetNegatesAnOrdinaryPID(t *testing.T) {
	got, err := groupSignalTarget(4321)
	if err != nil {
		t.Fatalf("groupSignalTarget(4321): %v", err)
	}
	if got != -4321 {
		t.Errorf("groupSignalTarget(4321) = %d, want -4321", got)
	}
}
