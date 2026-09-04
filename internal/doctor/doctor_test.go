package doctor

import (
	"testing"
)

// TestSeverityOrdering verifies that severity values can be compared.
func TestSeverityOrdering(t *testing.T) {
	if !(SeverityOK < SeverityWarn) {
		t.Error("SeverityOK should be less than SeverityWarn")
	}
	if !(SeverityWarn < SeverityFail) {
		t.Error("SeverityWarn should be less than SeverityFail")
	}
}

// TestCheckReturnsBasicFindings verifies that Check returns at least the
// go and git findings without panicking.
func TestCheckReturnsBasicFindings(t *testing.T) {
	findings := Check(".")
	if len(findings) == 0 {
		t.Error("Check returned no findings")
	}

	// Map findings by name for easier testing.
	findingsByName := make(map[string]Finding)
	for _, f := range findings {
		findingsByName[f.Name] = f
	}

	// Verify that we have go and git findings.
	if _, ok := findingsByName["go"]; !ok {
		t.Error("missing 'go' finding")
	}
	if _, ok := findingsByName["git"]; !ok {
		t.Error("missing 'git' finding")
	}
}

// TestCheckDoesNotPanic verifies that Check doesn't panic on any platform.
func TestCheckDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Check panicked: %v", r)
		}
	}()
	_ = Check(".")
}
