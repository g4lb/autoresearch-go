package verdict

import (
	"testing"

	"github.com/g4lb/autor3search-go/internal/bench"
)

// TestMutationEvidenceNoOpScenarioDiscards constructs the no-op scenario
// described in the tightening-KEEP-rule task: every benchmark statistically
// indistinguishable from baseline (large p-values, all well above alpha),
// geomean within noise of 1. Under the new rule this must DISCARD.
func TestMutationEvidenceNoOpScenarioDiscards(t *testing.T) {
	in := Input{
		Deltas: []bench.Delta{
			dp("A", 0.3, 0.6, 0.05),
			dp("B", -0.2, 0.7, 0.05),
			dp("C", 0.1, 0.9, 0.05),
			dp("D", -0.4, 0.5, 0.05),
		},
		Score:         0.9995,
		MaxRegressPct: 5,
		MinEffectPct:  1,
	}
	got := Decide(in)
	if got.Status != StatusDiscard {
		t.Fatalf("no-op scenario: got %s, want DISCARD", got.Status)
	}
}

// TestMutationEvidenceBorderlineFlipsToKeepWithoutMinEffect constructs a
// borderline case — significant at the corrected alpha/k, but only a 0.3%
// real improvement, well under the 1% MinEffectPct floor — and shows it
// DISCARDs under the real rule. It then re-derives the pre-fix rule inline
// (score < 1 with no minimum-effect floor) to prove that same borderline
// case would have wrongly KEPT under the old logic. This does not edit
// verdict.go; it exercises the documented degenerate case (MinEffectPct: 0
// reproduces the exact pre-fix "score < 1" behavior) to demonstrate the
// mutation's effect without permanently weakening Decide.
func TestMutationEvidenceBorderlineFlipsToKeepWithoutMinEffect(t *testing.T) {
	deltas := []bench.Delta{dp("A", -0.3, 0.001, 0.05)}
	borderlineScore := 0.997 // a genuine, significant, but trivial 0.3% win

	withFix := Decide(Input{Deltas: deltas, Score: borderlineScore, MaxRegressPct: 5, MinEffectPct: 1})
	if withFix.Status != StatusDiscard {
		t.Fatalf("with the min-effect floor: got %s, want DISCARD (0.3%% is below the 1%% floor)", withFix.Status)
	}

	withoutFix := Decide(Input{Deltas: deltas, Score: borderlineScore, MaxRegressPct: 5, MinEffectPct: 0})
	if withoutFix.Status != StatusKeep {
		t.Fatalf("with MinEffectPct: 0 (the old score<1 rule): got %s, want KEEP — "+
			"proving the min-effect floor is exactly what changed this outcome", withoutFix.Status)
	}
}
