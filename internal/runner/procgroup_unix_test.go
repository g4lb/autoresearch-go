//go:build unix

package runner

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestProcessGroupTimeoutOnUnix(t *testing.T) {
	// Verify that process group setup doesn't break timeout behavior.
	// On Unix, setupProcessGroup configures the cmd to kill entire process group
	// on timeout. This test verifies the timeout mechanism still works correctly.
	// The actual prevention of orphaned processes is verified by integration,
	// not unit tests (checking /proc for a specific PID across process boundaries
	// is inherently racy and requires external process inspection).
	var log bytes.Buffer
	r := New(tinyModule(t), 1*time.Millisecond, &log)
	res, err := r.Test(context.Background(), false)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, want true (exit=%d)", res.ExitCode)
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 on timeout", res.ExitCode)
	}
}
