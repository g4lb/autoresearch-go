package config

import (
	"os"
	"path/filepath"
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
		{"count below 2", func(c *Config) { c.Count = 1 }},
		{"negative regress", func(c *Config) { c.MaxRegressPct = -1 }},
		{"bad benchtime", func(c *Config) { c.Benchtime = "banana" }},
		{"bad timeout", func(c *Config) { c.Timeout = "banana" }},
		{"empty scope", func(c *Config) { c.Scope = nil }},
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
