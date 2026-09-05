package bench

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"golang.org/x/perf/benchmath"
)

// confidence is the confidence level for the reported median interval.
const confidence = 0.95

// Delta is the comparison of one benchmark's unit between baseline and candidate.
type Delta struct {
	Name string
	Unit string
	// BaseCenter and CandCenter are median values under AssumeNothing.
	BaseCenter float64
	CandCenter float64
	// Ratio is CandCenter/BaseCenter. Below 1 means the candidate is better
	// for lower-is-better units such as ns/op.
	Ratio float64
	// PctChange is (Ratio-1)*100.
	PctChange float64
	// P is the Mann-Whitney p-value; Alpha is the rejection threshold.
	P     float64
	Alpha float64
	// Significant reports P < Alpha.
	Significant  bool
	NBase, NCand int
	// Warnings are benchmath's own warnings about this comparison and the
	// two summaries behind it, deduplicated. They say when a comparison is
	// underpowered — too few observations for a finite confidence interval,
	// or too few for the U test to reach significance at all — which is
	// precisely when the numbers beside them must not be taken at face
	// value. benchmath's documentation says they should be shown to the
	// user, so they are carried out of this package rather than dropped.
	Warnings []string
}

// Compare tests one benchmark's unit across two measurement sets.
func Compare(base, cand *Set, name, unit string) (Delta, error) {
	bv, ok := base.Values(name, unit)
	if !ok {
		return Delta{}, fmt.Errorf("baseline has no %s for %s", unit, name)
	}
	cv, ok := cand.Values(name, unit)
	if !ok {
		return Delta{}, fmt.Errorf("candidate has no %s for %s", unit, name)
	}
	if len(bv) < 2 || len(cv) < 2 {
		return Delta{}, fmt.Errorf("%s: need at least 2 observations per side, got %d/%d", name, len(bv), len(cv))
	}

	th := benchmath.DefaultThresholds
	bs := benchmath.NewSample(bv, &th)
	cs := benchmath.NewSample(cv, &th)

	a := benchmath.AssumeNothing
	bSum := a.Summary(bs, confidence)
	cSum := a.Summary(cs, confidence)
	cmp := a.Compare(bs, cs)

	if bSum.Center == 0 {
		return Delta{}, fmt.Errorf("%s: baseline %s median is zero, cannot form a ratio", name, unit)
	}

	ratio := cSum.Center / bSum.Center
	return Delta{
		Name:        name,
		Unit:        unit,
		BaseCenter:  bSum.Center,
		CandCenter:  cSum.Center,
		Ratio:       ratio,
		PctChange:   (ratio - 1) * 100,
		P:           cmp.P,
		Alpha:       cmp.Alpha,
		Significant: cmp.P < cmp.Alpha,
		NBase:       cmp.N1,
		NCand:       cmp.N2,
		Warnings:    dedupeWarnings(bSum.Warnings, cSum.Warnings, cmp.Warnings),
	}, nil
}

// CompareAll compares every benchmark present in both sets, sorted by name.
// Every benchmark measured at baseline must also appear in the candidate.
// If a benchmark disappears from the candidate, it returns an error: a missing
// benchmark cannot be checked for regressions, which would hide a real problem.
func CompareAll(base, cand *Set, unit string) ([]Delta, error) {
	var out []Delta
	var missing []string
	for _, name := range base.Names() {
		if _, ok := cand.Values(name, unit); !ok {
			missing = append(missing, name)
			continue
		}
		d, err := Compare(base, cand, name, unit)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if len(missing) > 0 {
		// Sort for deterministic error message
		sort.Strings(missing)
		return nil, fmt.Errorf("benchmark(s) measured at baseline but missing from the candidate: %v — "+
			"a benchmark that disappears cannot be checked for regressions", missing)
	}
	if len(out) == 0 {
		return nil, errors.New("no benchmark appears in both baseline and candidate")
	}
	return out, nil
}

// GeoMean returns the geometric mean of the deltas' ratios. This is the single
// score the agent optimizes: below 1 is an overall speedup.
func GeoMean(deltas []Delta) (float64, error) {
	if len(deltas) == 0 {
		return 0, errors.New("geomean of an empty delta set")
	}
	var sum float64
	for _, d := range deltas {
		if d.Ratio <= 0 {
			return 0, fmt.Errorf("%s: non-positive ratio %v", d.Name, d.Ratio)
		}
		sum += math.Log(d.Ratio)
	}
	return math.Exp(sum / float64(len(deltas))), nil
}

// dedupeWarnings flattens benchmath's warning lists into messages, dropping
// repeats. The baseline and candidate summaries warn about the same sample
// size in the same words, and printing that twice per benchmark buries the
// distinct warnings among duplicates.
func dedupeWarnings(lists ...[]error) []string {
	var out []string
	seen := map[string]bool{}
	for _, list := range lists {
		for _, err := range list {
			msg := err.Error()
			if seen[msg] {
				continue
			}
			seen[msg] = true
			out = append(out, msg)
		}
	}
	return out
}
