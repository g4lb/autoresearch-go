// Package config loads and validates the autor3search-go run configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Path is the config location relative to the repository root.
const Path = ".autor3search/config.yaml"

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
	// MinEffectPct is the smallest geomean improvement, as a percentage,
	// that a KEEP will accept: the score must be below 1 - MinEffectPct/100.
	// A change that clears significance but is smaller than this is treated
	// as not worth a commit in an unattended loop.
	MinEffectPct float64 `yaml:"min_effect_pct"`
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
		MinEffectPct:  1.0,
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

// minCount is the smallest Count at which the significance test used by
// internal/bench (an exact Mann-Whitney rank-sum test) can ever report
// p < 0.05, no matter how large or how clean the improvement is. At n=2 or
// n=3 measured rounds per side, the best achievable two-sided p-value is
// 0.3333 and 0.1 respectively — both above the default alpha of 0.05 — so
// verdict.Decide would discard every single experiment, KEEP or not, with
// nothing in the output explaining why. See internal/pipeline's eval tests
// for how this was found.
const minCount = 4

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if c.Count < minCount {
		return fmt.Errorf("count must be at least %d: the significance test cannot report p < 0.05 "+
			"with fewer than %d measured rounds per side no matter how large the improvement is, so "+
			"every experiment would be discarded regardless of what changed (the default is 10)", minCount, minCount)
	}
	if c.MaxRegressPct < 0 {
		return errors.New("max_regress_pct must not be negative")
	}
	if c.MinEffectPct < 0 || c.MinEffectPct >= 100 {
		return errors.New("min_effect_pct must be at least 0 and less than 100")
	}
	if len(c.Scope) == 0 {
		return errors.New("scope must list at least one path pattern")
	}
	for _, s := range c.Scope {
		if strings.TrimSpace(s) == "" {
			return errors.New("scope must not contain an empty or whitespace-only entry")
		}
	}
	if _, err := time.ParseDuration(c.Benchtime); err != nil {
		if isBenchCountForm(c.Benchtime) {
			return fmt.Errorf("benchtime %q uses go test -benchtime's fixed-iteration-count form (Nx), "+
				"which is deliberately unsupported here: a fixed count makes rounds incomparable, because "+
				"a candidate that is twice as fast finishes in half the wall time and is therefore measured "+
				"under different thermal conditions — exactly what the interleaved A/B design exists to "+
				"eliminate. Use a duration instead, e.g. benchtime: 1s", c.Benchtime)
		}
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

// benchCountForm matches go test -benchtime's fixed-iteration-count form,
// e.g. "100x": one or more digits followed by a literal "x". That form is a
// legal value for the real `go test -benchtime` flag, which is exactly why
// Validate's rejection of it needs to explain itself rather than just fail
// to parse as a duration — a user who wrote it did not make a typo.
var benchCountForm = regexp.MustCompile(`^[0-9]+x$`)

// isBenchCountForm reports whether s is go test -benchtime's Nx count form.
func isBenchCountForm(s string) bool {
	return benchCountForm.MatchString(s)
}

// TimeoutDuration parses Timeout.
func (c Config) TimeoutDuration() (time.Duration, error) {
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 0, fmt.Errorf("timeout %q is not a duration: %w", c.Timeout, err)
	}
	return d, nil
}
