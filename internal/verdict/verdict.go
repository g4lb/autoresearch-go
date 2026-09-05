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
	ReasonBelowMinEffect   Reason = "improvement_below_min_effect"
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
	// MinEffectPct is the smallest geomean improvement, as a percentage,
	// that qualifies for KEEP: Score must be below 1 - MinEffectPct/100.
	// The zero value requires only Score < 1, matching the pre-Bonferroni
	// rule; internal/config.Default sets this to 1.0 for real runs.
	MinEffectPct float64
}

// Result is the harness's answer for one experiment.
type Result struct {
	Status      Status        `json:"status"`
	Reason      Reason        `json:"reason"`
	Score       float64       `json:"score"`
	Message     string        `json:"message"`
	Regressions []bench.Delta `json:"regressions,omitempty"`
	// Warnings say when the measurement behind this result is too weak to
	// support it: benchmath's own warnings about each comparison, plus the
	// harness's check that a KEEP was statistically reachable at all. They
	// never change the decision — they explain what it can and cannot mean.
	Warnings []string `json:"warnings,omitempty"`
}

// Gate builds a Result for a failed correctness stage, before measurement.
func Gate(status Status, reason Reason, message string) Result {
	return Result{Status: status, Reason: reason, Message: message}
}

// Decide applies the scoring rules:
//
//  1. Any regression significant at the raw, UNCORRECTED alpha (Delta.
//     Significant) and larger than MaxRegressPct rejects the change,
//     however good the overall score. This check deliberately does NOT
//     apply the Bonferroni correction from rule 2: Bonferroni only ever
//     makes it harder to call a result significant, and applying it here
//     would make the guard less sensitive to harm — backwards from what a
//     guard is for. The asymmetry is intentional: be conservative about
//     accepting a win, be liberal about catching a regression.
//  2. Otherwise keep only when BOTH:
//     a. the score is a real speedup by at least MinEffectPct — Score must
//     be below 1 - MinEffectPct/100, not merely below 1. A result that is
//     technically significant but trivially small is not worth a commit
//     in an unattended loop.
//     b. at least one benchmark improved at the Bonferroni-corrected
//     threshold alpha/k, where k is the number of benchmarks compared in
//     this experiment (len(in.Deltas)). Comparing k benchmarks against the
//     same uncorrected alpha inflates the family-wise false-positive rate
//     (with k=4 benchmarks, roughly an 18% chance at least one shows a
//     spurious "significant" improvement even when nothing changed);
//     dividing alpha by k is the standard correction.
//
// A change that clears 2b but misses 2a discards with
// ReasonBelowMinEffect rather than ReasonNoImprovement: it measurably
// worked, it was just too small to bank.
//
// Delta.Significant always means "significant at the raw, uncorrected
// alpha" — that stays the honest statistic for a human or agent reading the
// report. The Bonferroni correction applied in rule 2b is a KEEP-decision
// threshold layered on top, not a redefinition of what "significant" means.
func Decide(in Input) Result {
	// k is the number of benchmarks compared in this experiment — the
	// family size for the Bonferroni correction in rule 2b. len(in.Deltas)
	// is never 0 in practice (bench.CompareAll errors out on an empty
	// comparison), but guard against division by zero defensively anyway.
	k := len(in.Deltas)
	if k < 1 {
		k = 1
	}
	warnings := measurementWarnings(in.Deltas, k)

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
			Warnings:    warnings,
		}
	}

	improved := false
	for _, d := range in.Deltas {
		correctedAlpha := d.Alpha / float64(k)
		if d.PctChange < 0 && d.P < correctedAlpha {
			improved = true
			break
		}
	}
	minEffectThreshold := 1 - in.MinEffectPct/100
	if in.Score < minEffectThreshold && improved {
		return Result{
			Status:   StatusKeep,
			Reason:   ReasonImproved,
			Score:    in.Score,
			Message:  fmt.Sprintf("score %.4f (%+.2f%%)", in.Score, (in.Score-1)*100),
			Warnings: warnings,
		}
	}

	// Distinguish "your idea did nothing" from "your idea worked, but by
	// less than the minimum effect size". Both discard, but they call for
	// different next moves, and reporting the second as the first would
	// tell an agent its change had no effect when it measurably did.
	if improved && in.Score < 1 {
		return Result{
			Status: StatusDiscard,
			Reason: ReasonBelowMinEffect,
			Score:  in.Score,
			Message: fmt.Sprintf("score %.4f (%+.2f%%), a real improvement but below the %.1f%% minimum effect size",
				in.Score, (in.Score-1)*100, in.MinEffectPct),
			Warnings: warnings,
		}
	}
	return Result{
		Status:   StatusDiscard,
		Reason:   ReasonNoImprovement,
		Score:    in.Score,
		Message:  fmt.Sprintf("score %.4f (%+.2f%%), no significant improvement", in.Score, (in.Score-1)*100),
		Warnings: warnings,
	}
}

// measurementWarnings gathers everything that qualifies how far the numbers
// in a Result can be trusted: benchmath's own warnings about each comparison,
// then the harness's check that a KEEP was statistically reachable at all.
func measurementWarnings(deltas []bench.Delta, k int) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range deltas {
		for _, w := range d.Warnings {
			if seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	if w, ok := unreachableAlphaWarning(deltas, k); ok {
		out = append(out, w)
	}
	return out
}

// unreachableAlphaWarning reports when rule 2b cannot be satisfied by any
// result whatsoever, so the run is incapable of a KEEP before it starts.
//
// The Mann-Whitney U test has a floor on the p-value it can produce for a
// given pair of sample sizes: the two samples can be maximally separated and
// the test still only reaches 2/C(n1+n2, n1), because that is the fraction of
// orderings at least as extreme as the observed one. If the Bonferroni-
// corrected threshold alpha/k falls below that floor for EVERY benchmark, no
// benchmark can clear it and every experiment discards no matter what the
// agent does. config.Validate enforces a count floor for k=1; this is the
// same footgun at k benchmarks, which the validator cannot see because it
// does not know how many benchmarks will be compared.
//
// KEEP needs only one benchmark to clear the threshold, so this warns only
// when none of them can.
func unreachableAlphaWarning(deltas []bench.Delta, k int) (string, bool) {
	if len(deltas) == 0 {
		return "", false
	}
	worstN := 0
	alpha := 0.0
	for _, d := range deltas {
		corrected := d.Alpha / float64(k)
		if minAchievableP(d.NBase, d.NCand) < corrected {
			return "", false // this one can clear it; that is enough for a KEEP
		}
		n := d.NBase
		if d.NCand < n {
			n = d.NCand
		}
		if n > worstN {
			worstN, alpha = n, d.Alpha
		}
	}
	need := countForAlpha(alpha / float64(k))
	msg := fmt.Sprintf("no KEEP was reachable: comparing %d benchmark(s) corrects the significance "+
		"threshold to %.5f, but with %d rounds per side the test cannot produce a p-value below %.5f "+
		"however large the improvement is", k, alpha/float64(k), worstN, minAchievableP(worstN, worstN))
	if need > 0 {
		msg += fmt.Sprintf(" — raise count to at least %d", need)
	} else {
		msg += " — raise count, or measure fewer benchmarks"
	}
	return msg, true
}

// minAchievableP is the smallest two-sided p-value the Mann-Whitney U test
// can return for samples of size n1 and n2: 2/C(n1+n2, n1). It matches
// benchmath's generated uTestMinP table exactly (0.3333 at 2, 0.1000 at 3,
// 0.02857 at 4, 0.00794 at 5) without duplicating it.
func minAchievableP(n1, n2 int) float64 {
	if n1 < 1 || n2 < 1 {
		return 1
	}
	p := 2 / binom(n1+n2, n1)
	if p > 1 {
		return 1
	}
	return p
}

// binom returns C(n, k) as a float64, multiplying and dividing in step so
// the intermediate value stays near the result rather than overflowing
// through a factorial.
func binom(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	c := 1.0
	for i := 0; i < k; i++ {
		c = c * float64(n-i) / float64(i+1)
	}
	return c
}

// countForAlpha returns the smallest number of rounds per side at which the
// U test can produce a p-value below alpha, or 0 when no practical count
// does (the search stops at 50, far past any sensible benchmark budget).
func countForAlpha(alpha float64) int {
	for n := 2; n <= 50; n++ {
		if minAchievableP(n, n) < alpha {
			return n
		}
	}
	return 0
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
