package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/g4lb/autor3search-go/internal/state"
)

// TestMain redirects every run's out-of-tree state into a temporary
// directory for the whole package.
//
// Without it these tests write into the developer's REAL user cache, one
// directory per temporary repository, and never clean up — thousands of
// them accumulate over a project's life. t.Setenv cannot do this job: the
// state home has to be set before any test runs and stay set for all of
// them, and t.Setenv also forbids t.Parallel in every test that calls it.
func TestMain(m *testing.M) {
	os.Exit(runWithTempStateHome(m))
}

// runWithTempStateHome is split out so the temporary directory is removed by
// a deferred call, which os.Exit in TestMain would otherwise skip.
func runWithTempStateHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "autor3search-go-test-state-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp state home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	if err := os.Setenv(state.StateHomeEnv, home); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", state.StateHomeEnv, err)
		return 1
	}
	return m.Run()
}
