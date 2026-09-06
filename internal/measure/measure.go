// Package measure collects benchmark observations from a baseline and a
// candidate tree in an interleaved order.
//
// Interleaving is the core measurement discipline of autor3search-go.
// Comparing a candidate measured now against a baseline measured minutes ago
// attributes CPU thermal drift, frequency scaling and background load to the
// code change. Alternating the two sides within a single session cancels that
// drift, because both sides experience the same conditions.
package measure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/g4lb/autor3search-go/internal/bench"
	"github.com/g4lb/autor3search-go/internal/runner"
)

// RoundFunc produces one round of measurements for one side.
type RoundFunc func(ctx context.Context, round int) (*bench.Set, error)

// Interleave runs base and cand alternately for the given number of rounds and
// accumulates their observations. When warmup is true an extra leading round is
// run and discarded, absorbing first-touch effects such as cold caches and
// on-demand compilation.
func Interleave(ctx context.Context, rounds int, warmup bool, base, cand RoundFunc) (*bench.Set, *bench.Set, error) {
	if rounds < 2 {
		return nil, nil, fmt.Errorf("need at least 2 measured rounds, got %d", rounds)
	}
	total := rounds
	if warmup {
		total++
	}

	baseSet, candSet := bench.NewSet(), bench.NewSet()
	for i := 0; i < total; i++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		b, err := base(ctx, i)
		if err != nil {
			return nil, nil, fmt.Errorf("baseline round %d: %w", i, err)
		}
		c, err := cand(ctx, i)
		if err != nil {
			return nil, nil, fmt.Errorf("candidate round %d: %w", i, err)
		}
		if warmup && i == 0 {
			continue
		}
		baseSet.Add(b)
		candSet.Add(c)
	}
	return baseSet, candSet, nil
}

// Options configures Run.
type Options struct {
	// BaseDir and CandDir are the two worktrees to compare.
	BaseDir, CandDir string
	// Pattern is the -bench regexp.
	Pattern string
	// Benchtime is the -benchtime value.
	Benchtime string
	// Rounds is the number of measured rounds per side.
	Rounds int
	// Warmup adds a discarded leading round.
	Warmup bool
	// Timeout bounds each individual round.
	Timeout time.Duration
	// Env replaces the environment for benchmark subprocesses.
	Env []string
	// Log receives subprocess output. May be nil.
	Log io.Writer
}

// Run measures both worktrees with real go test invocations.
func Run(ctx context.Context, o Options) (*bench.Set, *bench.Set, error) {
	if o.Pattern == "" {
		return nil, nil, errors.New("measure: empty benchmark pattern")
	}
	side := func(dir string) RoundFunc {
		r := runner.New(dir, o.Timeout, o.Log)
		r.Env = o.Env
		return func(ctx context.Context, round int) (*bench.Set, error) {
			res, err := r.Bench(ctx, o.Pattern, o.Benchtime)
			if err != nil {
				return nil, err
			}
			if res.TimedOut {
				return nil, fmt.Errorf("benchmark round timed out after %s in %s", o.Timeout, dir)
			}
			if !res.OK() {
				return nil, fmt.Errorf("benchmark round failed in %s (exit %d):\n%s", dir, res.ExitCode, res.Tail(30))
			}
			set, err := bench.Parse(bytesReader(res.Stdout))
			if err != nil {
				return nil, err
			}
			if len(set.Names()) == 0 {
				return nil, fmt.Errorf("no benchmarks matched %q in %s", o.Pattern, dir)
			}
			return set, nil
		}
	}
	return Interleave(ctx, o.Rounds, o.Warmup, side(o.BaseDir), side(o.CandDir))
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
