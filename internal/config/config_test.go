package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() invalid: %v", err)
	}
}

func TestLoadAppliesDefaultsForOmittedFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte("benchmarks:\n  - BenchmarkParse\nscope:\n  - ./internal/...\n"), 0o644)

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Count != Default().Count {
		t.Errorf("Count = %d, want default %d", got.Count, Default().Count)
	}
	if got.MaxRegressPct != Default().MaxRegressPct {
		t.Errorf("MaxRegressPct = %v, want default %v", got.MaxRegressPct, Default().MaxRegressPct)
	}
	if len(got.Benchmarks) != 1 || got.Benchmarks[0] != "BenchmarkParse" {
		t.Errorf("Benchmarks = %v, want [BenchmarkParse]", got.Benchmarks)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"count below minimum", func(c *Config) { c.Count = 1 }},
		{"count below significance floor", func(c *Config) { c.Count = 3 }},
		{"negative regress", func(c *Config) { c.MaxRegressPct = -1 }},
		{"bad benchtime", func(c *Config) { c.Benchtime = "banana" }},
		{"count-form benchtime", func(c *Config) { c.Benchtime = "100x" }},
		{"bad timeout", func(c *Config) { c.Timeout = "banana" }},
		{"empty scope", func(c *Config) { c.Scope = nil }},
		{"empty scope entry", func(c *Config) { c.Scope = []string{"./...", ""} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("Validate() = nil, want error")
			}
		})
	}
}

func TestValidateExplainsWhyCountHasAFloor(t *testing.T) {
	// count: 3 looks like a small, reasonable trim to a user tuning for
	// speed, but the significance test used by internal/bench cannot ever
	// report p < 0.05 below 4 rounds per side — every experiment would
	// silently DISCARD all night. The error must say so, not just refuse.
	c := Default()
	c.Count = 3
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for count: 3, want error")
	}
	if !strings.Contains(err.Error(), "p < 0.05") {
		t.Errorf("error = %q, want it to explain the significance floor", err.Error())
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("error = %q, want it to mention the default of 10", err.Error())
	}
}

func TestValidateAcceptsTheSignificanceFloor(t *testing.T) {
	c := Default()
	c.Count = 4
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() with count: 4 (the floor) = %v, want nil", err)
	}
}

func TestValidateExplainsWhyTheCountFormIsRejected(t *testing.T) {
	// go test -benchtime accepts a fixed-iteration-count form ("100x"),
	// which is a perfectly legal value for the real flag — so the rejection
	// here must explain that it is a deliberate policy decision, not just
	// fail to parse as a duration and leave the user thinking they made a
	// typo.
	c := Default()
	c.Benchtime = "100x"
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for benchtime: 100x, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "100x") {
		t.Errorf("error = %q, want it to name the offending value", msg)
	}
	if !strings.Contains(msg, "count") && !strings.Contains(msg, "Nx") {
		t.Errorf("error = %q, want it to name the count form", msg)
	}
	if !strings.Contains(msg, "1s") {
		t.Errorf("error = %q, want a valid example such as \"1s\"", msg)
	}
}

func TestValidateStillRejectsGenuinelyBadBenchtime(t *testing.T) {
	// A plain typo must still be reported as a parse failure, not
	// misdiagnosed as the deliberately-unsupported count form.
	c := Default()
	c.Benchtime = "banana"
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for benchtime: banana, want error")
	}
	if strings.Contains(err.Error(), "count") {
		t.Errorf("error = %q, want a plain parse-failure message, not the count-form explanation", err.Error())
	}
}

func TestTimeoutDuration(t *testing.T) {
	c := Default()
	c.Timeout = "90s"
	d, err := c.TimeoutDuration()
	if err != nil {
		t.Fatalf("TimeoutDuration: %v", err)
	}
	if d != 90*time.Second {
		t.Errorf("d = %v, want 90s", d)
	}
}
