package profile

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptureRunsBenchmarksAndProducesTopOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	// Run against testdata/demo with a short benchtime to keep test fast.
	// We use the absolute path from the testdata directory.
	destDir := t.TempDir()
	ctx := context.Background()
	report, err := Capture(ctx, "../../testdata/demo", "Benchmark.*", "20ms", destDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	// Verify that we got non-empty top output mentioning CountWords
	if report.CPUTop == "" {
		t.Error("CPUTop is empty")
	}
	if !strings.Contains(report.CPUTop, "CountWords") {
		t.Errorf("CPUTop does not mention CountWords:\n%s", report.CPUTop)
	}

	// Verify that we got non-empty allocations output
	if report.MemTop == "" {
		t.Error("MemTop is empty")
	}

	// Verify that we have profile paths
	if report.CPUPath == "" {
		t.Error("CPUPath is empty")
	}
	if report.MemPath == "" {
		t.Error("MemPath is empty")
	}

	// CRITICAL: Verify that the profile files actually exist and are non-empty.
	// This test catches the bug where Capture deleted files before returning.
	cpuInfo, err := os.Stat(report.CPUPath)
	if err != nil {
		t.Errorf("CPU profile file does not exist: %v", err)
	} else if cpuInfo.Size() == 0 {
		t.Error("CPU profile file is empty")
	}

	memInfo, err := os.Stat(report.MemPath)
	if err != nil {
		t.Errorf("memory profile file does not exist: %v", err)
	} else if memInfo.Size() == 0 {
		t.Error("memory profile file is empty")
	}
}
