package verdict

import (
	"fmt"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/bench"
)

// d builds a Delta at the standard alpha (0.05). sig=true gives it a p-value
// (0.01) comfortably below alpha; sig=false gives it a p-value (0.5)
// comfortably above it. This keeps Significant (p < alpha, uncorrected)
// meaningful without every test having to pick its own p-value — use dp
// directly when a test needs to probe the Bonferroni boundary.
func d(name string, pct float64, sig bool) bench.Delta {
	p := 0.5
	if sig {
		p = 0.01
	}
	return dp(name, pct, p, 0.05)
}

// dp builds a Delta with an explicit p-value and alpha, for tests that need
// to sit precisely on one side or the other of alpha or a corrected alpha/k.
func dp(name string, pct, p, alpha float64) bench.Delta {
	return bench.Delta{
		Name: name, Unit: bench.UnitTime, PctChange: pct, Ratio: 1 + pct/100,
		P: p, Alpha: alpha, Significant: p < alpha,
	}
}

func TestDecideKeepsSignificantImprovement(t *testing.T) {
	got := Decide(Input{Deltas: []bench.Delta{d("A", -20, true)}, Score: 0.80, MaxRegressPct: 5, MinEffectPct: 1})
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved", got.Status, got.Reason)
	}
}

func TestDecideDiscardsInsignificantNoise(t *testing.T) {
	// Faster on paper but not significant: this is the noise trap.
	got := Decide(Input{Deltas: []bench.Delta{d("A", -3, false)}, Score: 0.97, MaxRegressPct: 5, MinEffectPct: 1})
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_improvement", got.Status, got.Reason)
	}
}

func TestDecideDiscardsWhenScoreIsWorse(t *testing.T) {
	got := Decide(Input{Deltas: []bench.Delta{d("A", 10, true)}, Score: 1.10, MaxRegressPct: 50, MinEffectPct: 1})
	if got.Status != StatusDiscard {
		t.Fatalf("got %s, want DISCARD", got.Status)
	}
}

func TestGuardRejectsSignificantRegressionEvenWhenScoreImproves(t *testing.T) {
	// A is much faster, B is significantly slower than the guard allows.
	in := Input{
		Deltas:        []bench.Delta{d("A", -40, true), d("B", 12, true)},
		Score:         0.82,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonGuardRegression {
		t.Fatalf("got %s/%s, want DISCARD/guard_regression", got.Status, got.Reason)
	}
	if len(got.Regressions) != 1 || got.Regressions[0].Name != "B" {
		t.Errorf("Regressions = %+v, want [B]", got.Regressions)
	}
	wantMsg := "regression guard tripped (limit +5.0%): B +12.0%"
	if got.Message != wantMsg {
		t.Errorf("Message = %q, want %q", got.Message, wantMsg)
	}
}

// TestGuardUsesUncorrectedAlphaEvenAtK4 proves the asymmetry the doc comment
// on Decide calls out: the regression guard must trip on a regression that
// is significant at the raw alpha, even though that same p-value would NOT
// clear the Bonferroni-corrected alpha/k bar that rule 2 applies to
// improvements. Applying the correction to the guard would make it less
// sensitive to harm, which is backwards.
func TestGuardUsesUncorrectedAlphaEvenAtK4(t *testing.T) {
	// B's p=0.03 is significant at alpha=0.05 (uncorrected) but would NOT
	// clear alpha/k = 0.05/4 = 0.0125 — if the guard applied the correction,
	// this regression would slip through.
	in := Input{
		Deltas: []bench.Delta{
			dp("A", -40, 0.001, 0.05),
			dp("B", 12, 0.03, 0.05),
			dp("C", -5, 0.001, 0.05),
			dp("D", -5, 0.001, 0.05),
		},
		Score:         0.80,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonGuardRegression {
		t.Fatalf("got %s/%s, want DISCARD/guard_regression — the guard must use the uncorrected alpha", got.Status, got.Reason)
	}
	if len(got.Regressions) != 1 || got.Regressions[0].Name != "B" {
		t.Errorf("Regressions = %+v, want [B]", got.Regressions)
	}
}

func TestGuardIgnoresInsignificantRegression(t *testing.T) {
	// B looks slower but the result is noise, so it must not block a keep.
	in := Input{
		Deltas:        []bench.Delta{d("A", -40, true), d("B", 12, false)},
		Score:         0.85,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	if got := Decide(in); got.Status != StatusKeep {
		t.Fatalf("got %s/%s, want KEEP", got.Status, got.Reason)
	}
}

func TestDecideDiscardsSignificantImprovementWhenScoreIsNotBetter(t *testing.T) {
	// One benchmark improved significantly, but the geomean is still >= 1.
	in := Input{
		Deltas:        []bench.Delta{d("A", -30, true)},
		Score:         1.05,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_improvement", got.Status, got.Reason)
	}
}

func TestGuardBoundaryAtExactLimit(t *testing.T) {
	// A delta at exactly MaxRegressPct should NOT trip the guard (> not >=).
	in := Input{
		Deltas:        []bench.Delta{d("A", -30, true), d("B", 5.0, true)},
		Score:         0.85,
		MaxRegressPct: 5.0,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved at exact boundary", got.Status, got.Reason)
	}
}

// --- minimum-effect truth table -----------------------------------------

func TestMinEffectJustAboveThresholdDiscards(t *testing.T) {
	// MinEffectPct: 1 requires score < 0.99. 0.991 is a hair on the wrong
	// side: significant, but too small to be worth a commit.
	in := Input{
		Deltas:        []bench.Delta{d("A", -0.9, true)},
		Score:         0.991,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	// The reason is improvement_below_min_effect, not
	// no_significant_improvement: the change measurably worked, it was
	// just too small to bank. Reporting it as "no improvement" would tell
	// an agent its idea did nothing when it demonstrably did.
	if got.Status != StatusDiscard || got.Reason != ReasonBelowMinEffect {
		t.Fatalf("got %s/%s, want DISCARD/improvement_below_min_effect for a significant win below the threshold", got.Status, got.Reason)
	}
	if !strings.Contains(got.Message, "below the 1.0% minimum effect size") {
		t.Errorf("Message = %q, want it to say the win was below the minimum effect size", got.Message)
	}
}

func TestNoImprovementKeepsItsOwnReason(t *testing.T) {
	// Nothing significant improved at all: this really is "no significant
	// improvement", and must not be mislabelled as a below-threshold win.
	in := Input{
		Deltas:        []bench.Delta{d("A", -0.4, false)},
		Score:         0.996,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_significant_improvement when nothing cleared significance", got.Status, got.Reason)
	}
}

func TestMinEffectJustBelowThresholdKeeps(t *testing.T) {
	// 0.989 clears score < 0.99 by a hair, with a genuinely significant delta.
	in := Input{
		Deltas:        []bench.Delta{d("A", -1.1, true)},
		Score:         0.989,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved for score just below the min-effect threshold", got.Status, got.Reason)
	}
}

func TestMinEffectZeroDisablesTheExtraRequirement(t *testing.T) {
	// MinEffectPct: 0 is the zero-value default (matches the pre-Bonferroni
	// rule): any score < 1 with a significant improvement qualifies.
	in := Input{
		Deltas:        []bench.Delta{d("A", -0.1, true)},
		Score:         0.999,
		MaxRegressPct: 5,
		MinEffectPct:  0,
	}
	got := Decide(in)
	if got.Status != StatusKeep {
		t.Fatalf("got %s/%s, want KEEP with MinEffectPct: 0", got.Status, got.Reason)
	}
}

// --- Bonferroni truth table -----------------------------------------------

func TestBonferroniPClearsAlphaButNotAlphaOverK(t *testing.T) {
	// p=0.03 clears the raw alpha (0.05) but not alpha/k = 0.05/4 = 0.0125.
	// At k=4 with no other benchmark clearing the corrected bar, this must
	// DISCARD even though the geomean looks like a real win and the delta
	// itself reports Significant: true.
	in := Input{
		Deltas: []bench.Delta{
			dp("A", -8, 0.03, 0.05),
			dp("B", 1, 0.5, 0.05),
			dp("C", 1, 0.5, 0.05),
			dp("D", 1, 0.5, 0.05),
		},
		Score:         0.98,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_improvement: p clears alpha but not the corrected alpha/k", got.Status, got.Reason)
	}
	if !in.Deltas[0].Significant {
		t.Fatal("sanity check: delta A must report Significant=true at the raw alpha")
	}
}

func TestBonferroniPClearsBothAlphaAndAlphaOverK(t *testing.T) {
	// p=0.005 clears both the raw alpha (0.05) and the corrected alpha/k
	// (0.0125 at k=4).
	in := Input{
		Deltas: []bench.Delta{
			dp("A", -8, 0.005, 0.05),
			dp("B", 1, 0.5, 0.05),
			dp("C", 1, 0.5, 0.05),
			dp("D", 1, 0.5, 0.05),
		},
		Score:         0.98,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved: p clears the corrected alpha/k", got.Status, got.Reason)
	}
}

func TestBonferroniKEqualsOneHasNoEffect(t *testing.T) {
	// A single benchmark: alpha/k == alpha, so a p that clears alpha alone
	// (like d("A", -8, true)'s p=0.01) must still KEEP.
	in := Input{
		Deltas:        []bench.Delta{d("A", -8, true)},
		Score:         0.98,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved at k=1", got.Status, got.Reason)
	}
}

func TestBonferroniKEqualsFourRequiresTighterP(t *testing.T) {
	// p=0.02 clears the raw alpha (0.05) but not the corrected alpha/4
	// (0.0125): at k=4 the same p-value that would pass at k=1 now fails.
	in := Input{
		Deltas: []bench.Delta{
			dp("A", -8, 0.02, 0.05),
			dp("B", 1, 0.5, 0.05),
			dp("C", 1, 0.5, 0.05),
			dp("D", 1, 0.5, 0.05),
		},
		Score:         0.98,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_improvement at k=4 with p=0.02 (clears alpha, not alpha/4)", got.Status, got.Reason)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		status Status
		want   int
	}{
		{StatusKeep, 0},
		{StatusDiscard, 1},
		{StatusFail, 2},
		{StatusCrash, 3},
		{Status("BOGUS"), 2},
	}
	for _, tt := range tests {
		if got := (Result{Status: tt.status}).ExitCode(); got != tt.want {
			t.Errorf("Status %q.ExitCode() = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestGateProducesFail(t *testing.T) {
	got := Gate(StatusFail, ReasonTests, "3 tests failed")
	if got.Status != StatusFail || got.Reason != ReasonTests || got.Message == "" {
		t.Fatalf("got %+v, want a populated FAIL", got)
	}
}

// The Bonferroni correction in rule 2b divides alpha by the number of
// benchmarks, but the Mann-Whitney U test has a floor on the p-value it can
// produce for a given sample size: with n observations per side the smallest
// attainable two-sided p is 2/C(2n,n), however far apart the two samples are.
// When alpha/k drops below that floor, no change can ever be kept — the run
// is incapable of a KEEP before it starts. config.Validate's count floor of 4
// only accounts for k=1, so a repository with several benchmarks can land
// here with a config the validator accepted.

func TestDecideWarnsWhenCorrectedAlphaIsUnreachable(t *testing.T) {
	// 7 benchmarks at count 5: corrected alpha is 0.05/7 = 0.00714, below
	// the 0.00794 floor for 5-vs-5.
	var deltas []bench.Delta
	for i := 0; i < 7; i++ {
		deltas = append(deltas, bench.Delta{
			Name: fmt.Sprintf("BenchmarkX%d", i), Unit: bench.UnitTime,
			Ratio: 0.5, PctChange: -50, P: 0.0079, Alpha: 0.05,
			Significant: true, NBase: 5, NCand: 5,
		})
	}
	res := Decide(Input{Deltas: deltas, Score: 0.5, MaxRegressPct: 5, MinEffectPct: 1})
	if len(res.Warnings) == 0 {
		t.Fatal("Warnings is empty; no KEEP is reachable at 7 benchmarks with 5 rounds per side")
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "no KEEP was reachable") {
		t.Errorf("Warnings = %q, want one saying no KEEP was reachable", res.Warnings)
	}
	// 6 rounds per side put the floor at 2/C(12,6) = 0.00216, below the
	// corrected 0.00714; 5 do not. Naming the number turns the warning into
	// an instruction rather than a complaint.
	if !strings.Contains(joined, "raise count to at least 6") {
		t.Errorf("Warnings = %q, want the smallest count that would work", res.Warnings)
	}
}

func TestDecideDoesNotWarnWhenTheSampleSizeSuffices(t *testing.T) {
	deltas := []bench.Delta{
		{Name: "BenchmarkA", Unit: bench.UnitTime, Ratio: 0.5, PctChange: -50,
			P: 0.0001, Alpha: 0.05, Significant: true, NBase: 10, NCand: 10},
		{Name: "BenchmarkB", Unit: bench.UnitTime, Ratio: 0.9, PctChange: -10,
			P: 0.0001, Alpha: 0.05, Significant: true, NBase: 10, NCand: 10},
	}
	res := Decide(Input{Deltas: deltas, Score: 0.67, MaxRegressPct: 5, MinEffectPct: 1})
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none: 0.05/2 is well above the 10-vs-10 floor", res.Warnings)
	}
	if res.Status != StatusKeep {
		t.Errorf("Status = %v, want KEEP; the warning check must not change decisions", res.Status)
	}
}

func TestDecideCarriesDeltaWarningsThrough(t *testing.T) {
	deltas := []bench.Delta{
		{Name: "BenchmarkA", Unit: bench.UnitTime, Ratio: 0.5, PctChange: -50,
			P: 0.0001, Alpha: 0.05, Significant: true, NBase: 10, NCand: 10,
			Warnings: []string{"need >= 6 samples for confidence interval at level 0.95"}},
	}
	res := Decide(Input{Deltas: deltas, Score: 0.5, MaxRegressPct: 5, MinEffectPct: 1})
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "confidence interval") {
		t.Errorf("Warnings = %q, want benchmath's own warning carried through", res.Warnings)
	}
}
