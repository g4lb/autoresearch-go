// Package pipeline runs one full evaluation: it gates correctness, measures
// a candidate against the pinned baseline, and returns a verdict.
//
// It lives here rather than in cmd/autor3search-go so it is testable
// without a process boundary — the eval command itself handles only flags,
// output formatting and the exit code.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/g4lb/autor3search-go/internal/bench"
	"github.com/g4lb/autor3search-go/internal/config"
	"github.com/g4lb/autor3search-go/internal/discover"
	"github.com/g4lb/autor3search-go/internal/freeze"
	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/measure"
	"github.com/g4lb/autor3search-go/internal/results"
	"github.com/g4lb/autor3search-go/internal/runner"
	"github.com/g4lb/autor3search-go/internal/scope"
	"github.com/g4lb/autor3search-go/internal/state"
	"github.com/g4lb/autor3search-go/internal/verdict"
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
	// Base is the reference point recorded by `baseline`. Eval may mutate
	// Base.MeasureCommit and persist the change to disk as a side effect of
	// a KEEP verdict — see advanceMeasurementBaseline. Base.Commit, the
	// frozen anchor, is only ever read, never written.
	Base *state.Baseline
	// Log receives subprocess output (build/vet/test/bench) and progress
	// notes such as which frozen files were restored. May be nil.
	Log io.Writer
}

// Measurements carries every unit compared for one experiment.
//
// Time is the scored metric: it is the only field verdict.Decide ever
// sees, and the only one that can trip the regression guard. Allocs is
// reported purely as a hint — program.md's idea bank points an agent at
// allocation counts as the most common lead for what to try next, and a
// human reading results.tsv leans on it to see WHY something got faster —
// but it never feeds the verdict. Keep that boundary: a future change that
// starts scoring Allocs would silently let allocation-only changes with no
// real latency improvement pass as KEEP.
type Measurements struct {
	// Time is the sec/op delta per benchmark. This is what Decide scores.
	Time []bench.Delta
	// Allocs is the allocs/op delta per benchmark, informational only. It
	// is nil when the comparison itself failed (see Eval) — a missing hint
	// is never a reason to discard an otherwise-valid experiment.
	Allocs []bench.Delta
}

// Eval runs one experiment: gate correctness, measure, score. It returns a
// terminal verdict.Result for every gate outcome and for every completed
// measurement; a non-nil error means the harness itself malfunctioned
// (I/O, git, a malformed baseline), not that the candidate was rejected.
func Eval(ctx context.Context, o Options) (verdict.Result, *Measurements, error) {
	timeout, err := o.Cfg.TimeoutDuration()
	if err != nil {
		return verdict.Result{}, nil, err
	}

	// 1. Scope. Checked before anything is restored or built, so an
	//    out-of-scope edit is reported as itself rather than as a build error.
	//
	// Deliberately diffs against o.Base.Commit — the FROZEN, never-advancing
	// anchor — and NOT o.Base.MeasureCommit, which moves after every KEEP.
	// Anchoring the scope gate to the run's true starting point means it
	// keeps re-validating the FULL accumulated diff on every single eval,
	// rather than trusting that anything already banked as a KEEP must have
	// been in scope. Anchoring it to the advancing MeasureCommit instead
	// would give an out-of-scope edit exactly one eval in which to be
	// caught: once past that single check it would become part of the
	// "already accepted" state and would never be looked at again — the
	// same class of moving-target bypass state.Baseline's doc comment
	// warns about for the measurement side. See state.Baseline for the
	// full reasoning.
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
				"revert it, or start a new run with 'autor3search-go baseline'", config.Path)), nil, nil
	}

	// 2. Restore frozen tests. Agent edits to tests are erased, not argued with.
	man, err := freeze.LoadManifest(filepath.Join(o.StateDir, freeze.ManifestPath))
	if err != nil {
		return verdict.Result{}, nil, err
	}
	restored, err := freeze.Restore(o.Root, filepath.Join(o.StateDir, freeze.StoreDir), man)
	if err != nil {
		// A frozen test file that has been replaced by a symlink is tampering,
		// not a harness malfunction: report it as a FAIL verdict (so the run
		// gets a row in results.tsv and an actionable message) rather than
		// propagating a Go error, which would abort the harness with no
		// signal at all. Any other Restore error (a genuine I/O failure)
		// still propagates as-is.
		if errors.Is(err, freeze.ErrSymlink) {
			return verdict.Gate(verdict.StatusFail, verdict.ReasonSymlinkSwap,
				fmt.Sprintf("%v — a frozen test file must remain a regular file; "+
					"restore it and rerun", err)), nil, nil
		}
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
				"add them before running 'autor3search-go baseline', or list them in config unfreeze", added)), nil, nil
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

	// 5b. Baseline worktree integrity. See state.StateDir's doc comment: an
	// agent could edit the pinned baseline worktree in place to make the
	// BASELINE itself slow, after which every candidate "improves" and every
	// experiment returns KEEP without optimizing anything. Detect that by
	// checking the pinned worktree's HEAD still matches the commit recorded
	// as the current measurement anchor.
	//
	// Compares against o.Base.MeasureCommit, not o.Base.Commit: MeasureCommit
	// is re-pointed to the newly kept commit (and the worktree moved to
	// match) after every KEEP — see advanceMeasurementBaseline below — so
	// comparing against the frozen o.Base.Commit here would fire spuriously
	// on the very next eval after any KEEP, once the worktree and the
	// original anchor have legitimately diverged.
	//
	// This is a DETECTION, not a prevention, and only a partial one: the
	// state directory lives outside the repo, but the agent runs as the same
	// OS user, so nothing stops it from editing the worktree in place, and
	// this check only catches that if the worktree's HEAD itself moves. A
	// determined same-user attacker who edits the worktree's tracked files
	// WITHOUT moving HEAD (e.g. `git commit --amend` back onto the same
	// hash after editing, or just dirtying the tree without committing) is
	// not caught here at all. Nor is an attacker who also rewrites
	// baseline.json's MeasureCommit field to match a genuinely different
	// HEAD they moved the worktree to — that rewrites the very value this
	// check compares against. Treat this as catching accidental clobbering
	// and a careless tamper, not as an airtight guarantee.
	worktreeDir := filepath.Join(o.StateDir, state.WorktreeName)
	worktreeHead, err := gitx.HeadCommit(worktreeDir)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	if worktreeHead != o.Base.MeasureCommit {
		return verdict.Gate(verdict.StatusFail, verdict.ReasonBaselineTampered,
			fmt.Sprintf("pinned baseline worktree HEAD is %s but the recorded measurement commit is %s — "+
				"the worktree no longer matches the baseline and this run's measurements cannot be trusted. "+
				"Start a fresh run with 'autor3search-go baseline'.", worktreeHead, o.Base.MeasureCommit)), nil, nil
	}

	// 6. Measure, interleaved against the pinned baseline worktree.
	baseSet, candSet, err := measure.Run(ctx, measure.Options{
		BaseDir:   worktreeDir,
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

	// 7. Score. Time is the scored metric: any failure here (including a
	// benchmark that vanished from the candidate) fails the whole call, per
	// bench.CompareAll's contract.
	timeDeltas, err := bench.CompareAll(baseSet, candSet, bench.UnitTime)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	score, err := bench.GeoMean(timeDeltas)
	if err != nil {
		return verdict.Result{}, nil, err
	}

	// Allocs is informational only (see Measurements). CompareAll is strict
	// about a benchmark disappearing from one side, which is right for the
	// scored metric but wrong here: a benchmark that legitimately makes zero
	// allocations on one side, or any other allocs-specific mismatch, must
	// not fail a real, correctly-measured experiment over a missing hint.
	allocsDeltas, err := bench.CompareAll(baseSet, candSet, bench.UnitAllocs)
	if err != nil {
		allocsDeltas = nil
		if o.Log != nil {
			fmt.Fprintf(o.Log, "allocs/op comparison unavailable, continuing without it: %v\n", err)
		}
	}

	result := verdict.Decide(verdict.Input{
		Deltas:        timeDeltas,
		Score:         score,
		MaxRegressPct: o.Cfg.MaxRegressPct,
		MinEffectPct:  o.Cfg.MinEffectPct,
	})

	// 8. Advance the measurement baseline on KEEP. Without this, every
	// experiment after the first kept one is measured against the run's
	// ORIGINAL commit forever, so a later no-op change that merely fails to
	// regress an EARLIER improvement still banks as KEEP. Re-pointing the
	// pinned worktree (and MeasureCommit) to the newly kept commit makes the
	// next eval answer "did THIS change help", not "is the tree still
	// better than when the run started".
	if result.Status == verdict.StatusKeep {
		if err := advanceMeasurementBaseline(o, worktreeDir); err != nil {
			return verdict.Result{}, nil, err
		}
	}

	return result, &Measurements{Time: timeDeltas, Allocs: allocsDeltas}, nil
}

// advanceMeasurementBaseline re-points the pinned baseline worktree at the
// candidate's own commit and persists that commit as the new
// state.Baseline.MeasureCommit, after a KEEP. See state.Baseline's doc
// comment for why MeasureCommit (advancing) and Commit (frozen) are two
// separate fields.
//
// A failure here is returned as a Go error, not folded into the verdict:
// per Eval's contract, a non-nil error means the harness itself
// malfunctioned, and every caller already treats that as a reason to stop
// rather than record a row and continue — which is exactly right here.
// Continuing to run further experiments against a worktree that no longer
// agrees with the recorded MeasureCommit (or a MeasureCommit that no longer
// agrees with the worktree) would silently corrupt every subsequent
// measurement in the run, which is precisely the class of bug this advance
// exists to fix in the first place. Should this call fail after the
// worktree has already moved but before the new MeasureCommit is
// persisted, the NEXT eval's worktree-integrity check (step 5b) will catch
// the resulting mismatch and fail loudly rather than measure against it
// silently.
func advanceMeasurementBaseline(o Options, worktreeDir string) error {
	newCommit, err := gitx.HeadCommit(o.Root)
	if err != nil {
		return fmt.Errorf("advance measurement baseline: read candidate HEAD: %w", err)
	}
	if err := gitx.CheckoutDetached(worktreeDir, newCommit); err != nil {
		return fmt.Errorf("advance measurement baseline: re-point pinned worktree to %s: %w", newCommit, err)
	}
	o.Base.MeasureCommit = newCommit
	if err := o.Base.Save(filepath.Join(o.StateDir, state.BaselineFile)); err != nil {
		return fmt.Errorf("advance measurement baseline: persist new measurement commit %s: %w", newCommit, err)
	}
	return nil
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
