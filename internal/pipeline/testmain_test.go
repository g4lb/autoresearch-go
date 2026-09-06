package pipeline

import (
	"fmt"
	"os"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/state"
)

// TestMain redirects every run's out-of-tree state into a temporary
// directory for the whole package, so these tests never write into the
// developer's real user cache. See cmd/autoresearch-go's TestMain for why
// this cannot be t.Setenv.
func TestMain(m *testing.M) {
	os.Exit(runWithTempStateHome(m))
}

// runWithTempStateHome is split out so the temporary directory is removed by
// a deferred call, which os.Exit in TestMain would otherwise skip.
func runWithTempStateHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "autoresearch-go-test-state-")
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
