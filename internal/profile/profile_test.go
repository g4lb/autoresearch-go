package profile

import (
	"context"
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
	ctx := context.Background()
	report, err := Capture(ctx, "../../testdata/demo", "Benchmark.*", "20ms", 30*time.Second)
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
}
