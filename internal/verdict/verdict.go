// Package verdict turns gate outcomes and measurements into a single decision.
package verdict

import (
	"fmt"
	"strings"

	"github.com/g4lb/autoresearch-go/internal/bench"
)

// Status is the terminal outcome of one experiment.
type Status string

// The four possible outcomes.
const (
	StatusKeep    Status = "KEEP"
	StatusDiscard Status = "DISCARD"
	StatusFail    Status = "FAIL"
	StatusCrash   Status = "CRASH"
)

// Reason explains a Status in a machine-readable way.
type Reason string

// Reasons, grouped by the stage that produces them.
const (
	ReasonImproved         Reason = "improved"
	ReasonNoImprovement    Reason = "no_significant_improvement"
	ReasonGuardRegression  Reason = "guard_regression"
	ReasonScope            Reason = "scope_violation"
	ReasonConfigChanged    Reason = "config_changed"
	ReasonNewTestFile      Reason = "new_test_file"
	ReasonSymlinkSwap      Reason = "symlink_swap"
	ReasonBaselineTampered Reason = "baseline_tampered"
	ReasonBuild            Reason = "build_failed"
	ReasonVet              Reason = "vet_failed"
	ReasonTests            Reason = "tests_failed"
	ReasonTimeout          Reason = "timeout"
)

// Input is everything Decide needs once all correctness gates have passed.
type Input struct {
	Deltas        []bench.Delta
	Score         float64
	MaxRegressPct float64
}

// Result is the harness's answer for one experiment.
type Result struct {
	Status      Status        `json:"status"`
	Reason      Reason        `json:"reason"`
	Score       float64       `json:"score"`
	Message     string        `json:"message"`
	Regressions []bench.Delta `json:"regressions,omitempty"`
}

// Gate builds a Result for a failed correctness stage, before measurement.
func Gate(status Status, reason Reason, message string) Result {
	return Result{Status: status, Reason: reason, Message: message}
}

// Decide applies the scoring rules:
//
//  1. Any statistically significant regression larger than MaxRegressPct
//     rejects the change, however good the overall score.
//  2. Otherwise keep only when the score is a real speedup AND at least one
//     benchmark improved significantly. Requiring significance is what stops
//     the agent from banking measurement noise as progress.
func Decide(in Input) Result {
	var regressions []bench.Delta
	for _, d := range in.Deltas {
		if d.Significant && d.PctChange > in.MaxRegressPct {
			regressions = append(regressions, d)
		}
	}
	if len(regressions) > 0 {
		var sb strings.Builder
		for i, d := range regressions {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%s %+.1f%%", d.Name, d.PctChange)
		}
		return Result{
			Status:      StatusDiscard,
			Reason:      ReasonGuardRegression,
			Score:       in.Score,
			Message:     fmt.Sprintf("regression guard tripped (limit %+.1f%%): %s", in.MaxRegressPct, sb.String()),
			Regressions: regressions,
		}
	}

	improved := false
	for _, d := range in.Deltas {
		if d.Significant && d.PctChange < 0 {
			improved = true
			break
		}
	}
	if in.Score < 1 && improved {
		return Result{
			Status:  StatusKeep,
			Reason:  ReasonImproved,
			Score:   in.Score,
			Message: fmt.Sprintf("score %.4f (%+.2f%%)", in.Score, (in.Score-1)*100),
		}
	}
	return Result{
		Status:  StatusDiscard,
		Reason:  ReasonNoImprovement,
		Score:   in.Score,
		Message: fmt.Sprintf("score %.4f (%+.2f%%), no significant improvement", in.Score, (in.Score-1)*100),
	}
}

// ExitCode maps a Status to the process exit code documented in program.md.
func (r Result) ExitCode() int {
	switch r.Status {
	case StatusKeep:
		return 0
	case StatusDiscard:
		return 1
	case StatusFail:
		return 2
	case StatusCrash:
		return 3
	}
	return 2
}
