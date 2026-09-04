// Package config loads and validates the autoresearch-go run configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Path is the config location relative to the repository root.
const Path = ".autoresearch/config.yaml"

// Config controls what is measured, what may be edited, and how strictly.
type Config struct {
	// Benchmarks is the declared benchmark set. Empty means all discovered.
	Benchmarks []string `yaml:"benchmarks"`
	// Scope lists the package patterns the agent may modify.
	Scope []string `yaml:"scope"`
	// Count is the number of measured rounds per side.
	Count int `yaml:"count"`
	// Benchtime is passed to go test -benchtime.
	Benchtime string `yaml:"benchtime"`
	// Race enables -race in the correctness gate.
	Race bool `yaml:"race"`
	// MaxRegressPct is the largest tolerated significant regression, percent.
	MaxRegressPct float64 `yaml:"max_regress_pct"`
	// GOMAXPROCS pins parallelism during measurement. 0 leaves it alone.
	GOMAXPROCS int `yaml:"gomaxprocs"`
	// Timeout bounds each subprocess phase.
	Timeout string `yaml:"timeout"`
	// Unfreeze lists test files deliberately exempted from freezing.
	Unfreeze []string `yaml:"unfreeze"`
}

// Default returns the configuration used when a field is omitted.
func Default() Config {
	return Config{
		Scope:         []string{"./..."},
		Count:         10,
		Benchtime:     "1s",
		Race:          true,
		MaxRegressPct: 5.0,
		Timeout:       "15m",
	}
}

// Load reads a config file, applying defaults for omitted fields.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	c := Default()
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return c, nil
}

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if c.Count < 2 {
		return errors.New("count must be at least 2 for a meaningful comparison")
	}
	if c.MaxRegressPct < 0 {
		return errors.New("max_regress_pct must not be negative")
	}
	if len(c.Scope) == 0 {
		return errors.New("scope must list at least one path pattern")
	}
	if _, err := time.ParseDuration(c.Benchtime); err != nil {
		return fmt.Errorf("benchtime %q is not a duration: %w", c.Benchtime, err)
	}
	if _, err := c.TimeoutDuration(); err != nil {
		return err
	}
	if c.GOMAXPROCS < 0 {
		return errors.New("gomaxprocs must not be negative")
	}
	return nil
}

// TimeoutDuration parses Timeout.
func (c Config) TimeoutDuration() (time.Duration, error) {
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 0, fmt.Errorf("timeout %q is not a duration: %w", c.Timeout, err)
	}
	return d, nil
}
