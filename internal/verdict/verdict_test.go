package verdict

import (
	"testing"

	"github.com/g4lb/autoresearch-go/internal/bench"
)

func d(name string, pct float64, sig bool) bench.Delta {
	return bench.Delta{Name: name, Unit: bench.UnitTime, PctChange: pct, Ratio: 1 + pct/100, Significant: sig}
}

func TestDecideKeepsSignificantImprovement(t *testing.T) {
	got := Decide(Input{Deltas: []bench.Delta{d("A", -20, true)}, Score: 0.80, MaxRegressPct: 5})
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved", got.Status, got.Reason)
	}
}

func TestDecideDiscardsInsignificantNoise(t *testing.T) {
	// Faster on paper but not significant: this is the noise trap.
	got := Decide(Input{Deltas: []bench.Delta{d("A", -3, false)}, Score: 0.97, MaxRegressPct: 5})
	if got.Status != StatusDiscard || got.Reason != ReasonNoImprovement {
		t.Fatalf("got %s/%s, want DISCARD/no_improvement", got.Status, got.Reason)
	}
}

func TestDecideDiscardsWhenScoreIsWorse(t *testing.T) {
	got := Decide(Input{Deltas: []bench.Delta{d("A", 10, true)}, Score: 1.10, MaxRegressPct: 50})
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

func TestGuardIgnoresInsignificantRegression(t *testing.T) {
	// B looks slower but the result is noise, so it must not block a keep.
	in := Input{
		Deltas:        []bench.Delta{d("A", -40, true), d("B", 12, false)},
		Score:         0.85,
		MaxRegressPct: 5,
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
	}
	got := Decide(in)
	if got.Status != StatusKeep || got.Reason != ReasonImproved {
		t.Fatalf("got %s/%s, want KEEP/improved at exact boundary", got.Status, got.Reason)
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
