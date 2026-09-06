package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/state"
)

func TestStopRequestedFalseWhenNoSentinel(t *testing.T) {
	dir := t.TempDir()
	if state.StopRequested(dir) {
		t.Fatal("StopRequested = true with no sentinel written; want false")
	}
}

func TestRequestStopThenStopRequested(t *testing.T) {
	dir := t.TempDir()
	if err := state.RequestStop(dir); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if !state.StopRequested(dir) {
		t.Fatal("StopRequested = false after RequestStop; want true")
	}
}

func TestRequestStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := state.RequestStop(dir); err != nil {
			t.Fatalf("RequestStop #%d: %v", i+1, err)
		}
	}
	if !state.StopRequested(dir) {
		t.Fatal("StopRequested = false after two RequestStop calls; want true")
	}
}

func TestRequestStopCreatesMissingStateDir(t *testing.T) {
	// `stop` may run before the state dir exists on disk for a tag whose
	// baseline was interrupted; it must not fail with ENOENT.
	dir := filepath.Join(t.TempDir(), "not", "yet", "there")
	if err := state.RequestStop(dir); err != nil {
		t.Fatalf("RequestStop into a missing dir: %v", err)
	}
	if !state.StopRequested(dir) {
		t.Fatal("StopRequested = false after RequestStop created the dir; want true")
	}
}

func TestClearStopRemovesTheRequest(t *testing.T) {
	dir := t.TempDir()
	if err := state.RequestStop(dir); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if err := state.ClearStop(dir); err != nil {
		t.Fatalf("ClearStop: %v", err)
	}
	if state.StopRequested(dir) {
		t.Fatal("StopRequested = true after ClearStop; want false")
	}
}

func TestClearStopWithNoRequestIsNotAnError(t *testing.T) {
	if err := state.ClearStop(t.TempDir()); err != nil {
		t.Fatalf("ClearStop with nothing to clear: %v", err)
	}
}

func TestEvalRunningIsFalseWithNoClaim(t *testing.T) {
	_, running, err := state.EvalRunning(t.TempDir())
	if err != nil {
		t.Fatalf("EvalRunning: %v", err)
	}
	if running {
		t.Fatal("EvalRunning reported a running eval with no claim; want none")
	}
}

func TestReleasedClaimIsNotReportedAsRunning(t *testing.T) {
	// The whole point of claiming rather than just writing a pid: an eval
	// that has exited must not leave behind a pid `stop -force` would
	// signal, because the OS may have recycled it onto another process.
	dir := t.TempDir()
	release, err := state.ClaimEval(dir, 4321)
	if err != nil {
		t.Fatalf("ClaimEval: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, running, _ := state.EvalRunning(dir); running {
		t.Fatal("EvalRunning still reports a running eval after the claim was released")
	}
}

func TestEvalRunningRejectsGarbagePID(t *testing.T) {
	// A truncated or hand-edited pid file must not be read as a pid to
	// signal.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, state.EvalPIDFile), []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.EvalRunning(dir); err == nil {
		t.Fatal("EvalRunning accepted a non-numeric pid file; want an error")
	}
}

func TestEvalRunningRejectsNonPositivePID(t *testing.T) {
	// Negative pids signal a whole process group on Unix; a corrupt file
	// must never be turned into a group-wide kill.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, state.EvalPIDFile), []byte("-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.EvalRunning(dir); err == nil {
		t.Fatal("EvalRunning accepted pid -1; want an error")
	}
}
