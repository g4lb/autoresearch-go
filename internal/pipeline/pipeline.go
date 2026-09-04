// Package pipeline runs one full evaluation: it gates correctness, measures
// a candidate against the pinned baseline, and returns a verdict.
//
// It lives here rather than in cmd/autoresearch-go so it is testable
// without a process boundary — the eval command itself handles only flags,
// output formatting and the exit code.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/g4lb/autoresearch-go/internal/bench"
	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/discover"
	"github.com/g4lb/autoresearch-go/internal/freeze"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/measure"
	"github.com/g4lb/autoresearch-go/internal/results"
	"github.com/g4lb/autoresearch-go/internal/runner"
	"github.com/g4lb/autoresearch-go/internal/scope"
	"github.com/g4lb/autoresearch-go/internal/state"
	"github.com/g4lb/autoresearch-go/internal/verdict"
)

// RunLogName is the harness-owned scratch log inside the repository root.
// Subprocess output that could flood an unattended agent's context (the
// full stdout/stderr of go build/vet/test/bench) is written here by
// default rather than streamed to stdout. It is gitignored by `init` and
// is not itself part of the score.
const RunLogName = "run.log"

// Options carries everything one Eval call needs. The caller (the eval
// command) resolves all of these before invoking Eval: Root via
// gitx.Root, StateDir via state.StateDir keyed by the run tag taken from
// the current branch, Cfg via config.Load, and Base via state.LoadBaseline.
type Options struct {
	// Root is the repository root being evaluated.
	Root string
	// StateDir is the out-of-tree directory holding this run's frozen
	// tests, baseline record and pinned baseline worktree.
	StateDir string
	// Cfg is the run configuration loaded from the in-repo config file.
	Cfg config.Config
	// Base is the fixed reference point recorded by `baseline`.
	Base *state.Baseline
	// Log receives subprocess output (build/vet/test/bench) and progress
	// notes such as which frozen files were restored. May be nil.
	Log io.Writer
}

// Eval runs one experiment: gate correctness, measure, score. It returns a
// terminal verdict.Result for every gate outcome and for every completed
// measurement; a non-nil error means the harness itself malfunctioned
// (I/O, git, a malformed baseline), not that the candidate was rejected.
func Eval(ctx context.Context, o Options) (verdict.Result, []bench.Delta, error) {
	timeout, err := o.Cfg.TimeoutDuration()
	if err != nil {
		return verdict.Result{}, nil, err
	}

	// 1. Scope. Checked before anything is restored or built, so an
	//    out-of-scope edit is reported as itself rather than as a build error.
	changed, err := gitx.ChangedSince(o.Root, o.Base.Commit)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	m := scope.New(o.Cfg.Scope)
	for _, rel := range changed {
		// go.mod and go.sum are rejected regardless of scope. Changing
		// dependencies is a supply-chain decision a human makes, not something
		// an unattended overnight loop decides — and a swapped dependency would
		// change what is being measured, not just how fast it runs. The default
		// scope is "./...", which matches root files, so this cannot be left to
		// the scope patterns.
		if rel == "go.mod" || rel == "go.sum" {
			return verdict.Gate(verdict.StatusFail, verdict.ReasonScope,
				fmt.Sprintf("%s may not be modified: dependency changes are a human decision, "+
					"not an autonomous one", rel)), nil, nil
		}
		if rel == results.Path || rel == RunLogName || rel == config.Path {
			// Harness output, plus the human-owned config, which is
			// integrity-checked below rather than scope-checked.
			continue
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue // handled by restore, not by the scope gate
		}
		if !m.Match(rel) {
			return verdict.Gate(verdict.StatusFail, verdict.ReasonScope,
				fmt.Sprintf("%s is outside the allowed scope %v", rel, o.Cfg.Scope)), nil, nil
		}
	}

	// 1b. Config integrity. config.yaml lives in the repo because humans own
	//     it, which means the agent can reach it. Raising max_regress_pct or
	//     deleting entries from the benchmark set would defeat the guard, so
	//     the file is hashed at baseline and any change fails the run.
	cfgSum, err := sha256File(filepath.Join(o.Root, config.Path))
	if err != nil {
		return verdict.Result{}, nil, err
	}
	if cfgSum != o.Base.ConfigSHA256 {
		return verdict.Gate(verdict.StatusFail, verdict.ReasonConfigChanged,
			fmt.Sprintf("%s changed since baseline — scoring rules are fixed for a run; "+
				"revert it, or start a new run with 'autoresearch-go baseline'", config.Path)), nil, nil
	}

	// 2. Restore frozen tests. Agent edits to tests are erased, not argued with.
	man, err := freeze.LoadManifest(filepath.Join(o.StateDir, freeze.ManifestPath))
	if err != nil {
		return verdict.Result{}, nil, err
	}
	restored, err := freeze.Restore(o.Root, filepath.Join(o.StateDir, freeze.StoreDir), man)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	if len(restored) > 0 && o.Log != nil {
		fmt.Fprintf(o.Log, "restored %d frozen test file(s): %v\n", len(restored), restored)
	}

	// 2b. Reject test files that did not exist at baseline.
	//
	// Restore only rewrites files it froze, and the scope gate above skips
	// every _test.go. Without this check an agent could ADD a brand-new
	// _test.go — an easier benchmark, or a file shadowing a frozen one — and
	// neither gate would notice. Any _test.go absent from the manifest and
	// not explicitly unfrozen by a human is a hard failure.
	present, err := discover.TestFiles(o.Root, o.Cfg.Unfreeze)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	var added []string
	for _, rel := range present {
		if _, frozen := man.Files[rel]; !frozen {
			added = append(added, rel)
		}
	}
	if len(added) > 0 {
		return verdict.Gate(verdict.StatusFail, verdict.ReasonNewTestFile,
			fmt.Sprintf("test files not present at baseline: %v — the benchmark set is frozen; "+
				"add them before running 'autoresearch-go baseline', or list them in config unfreeze", added)), nil, nil
	}

	r := runner.New(o.Root, timeout, o.Log)

	// 3. Build.
	if res, err := r.Build(ctx); err != nil {
		return verdict.Result{}, nil, err
	} else if res.TimedOut {
		return verdict.Gate(verdict.StatusCrash, verdict.ReasonTimeout, "go build timed out"), nil, nil
	} else if !res.OK() {
		return verdict.Gate(verdict.StatusCrash, verdict.ReasonBuild, res.Tail(30)), nil, nil
	}

	// 4. Vet.
	if res, err := r.Vet(ctx); err != nil {
		return verdict.Result{}, nil, err
	} else if !res.OK() {
		return verdict.Gate(verdict.StatusFail, verdict.ReasonVet, res.Tail(30)), nil, nil
	}

	// 5. Tests. Correctness is never traded for speed.
	if res, err := r.Test(ctx, o.Cfg.Race); err != nil {
		return verdict.Result{}, nil, err
	} else if res.TimedOut {
		return verdict.Gate(verdict.StatusCrash, verdict.ReasonTimeout, "go test timed out"), nil, nil
	} else if !res.OK() {
		return verdict.Gate(verdict.StatusFail, verdict.ReasonTests, res.Tail(40)), nil, nil
	}

	// 6. Measure, interleaved against the pinned baseline worktree.
	baseSet, candSet, err := measure.Run(ctx, measure.Options{
		BaseDir:   filepath.Join(o.StateDir, state.WorktreeName),
		CandDir:   o.Root,
		Pattern:   o.Base.Pattern,
		Benchtime: o.Cfg.Benchtime,
		Rounds:    o.Cfg.Count,
		Warmup:    true,
		Timeout:   timeout,
		Env:       benchEnv(o.Cfg),
		Log:       o.Log,
	})
	if err != nil {
		return verdict.Gate(verdict.StatusCrash, verdict.ReasonBuild, err.Error()), nil, nil
	}

	// 7. Score.
	deltas, err := bench.CompareAll(baseSet, candSet, bench.UnitTime)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	score, err := bench.GeoMean(deltas)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	return verdict.Decide(verdict.Input{
		Deltas:        deltas,
		Score:         score,
		MaxRegressPct: o.Cfg.MaxRegressPct,
	}), deltas, nil
}

// benchEnv returns the environment for benchmark subprocesses: the
// process's own environment, plus a pinned GOMAXPROCS when configured.
// Pinning parallelism is what keeps the "-N" benchmark-name suffix
// identical between the baseline and candidate sides, which CompareAll
// requires to match names up.
func benchEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.GOMAXPROCS > 0 {
		env = append(env, fmt.Sprintf("GOMAXPROCS=%d", cfg.GOMAXPROCS))
	}
	return env
}

// sha256File hashes a file's contents, hex-encoded.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
