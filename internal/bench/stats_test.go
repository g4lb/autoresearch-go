package bench

import (
	"math"
	"strings"
	"testing"
)

func setOf(t *testing.T, name string, vals ...float64) *Set {
	t.Helper()
	s := NewSet()
	for _, v := range vals {
		s.record(name, UnitTime, v)
	}
	return s
}

func TestCompareDetectsSignificantImprovement(t *testing.T) {
	base := setOf(t, "BenchmarkX", 412.3, 401.1, 409.9, 415.0, 407.2)
	cand := setOf(t, "BenchmarkX", 321.0, 318.4, 325.9, 319.1, 322.7)

	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !d.Significant {
		t.Errorf("Significant = false, want true (P=%v alpha=%v)", d.P, d.Alpha)
	}
	if d.Ratio >= 1 {
		t.Errorf("Ratio = %v, want < 1 for a speedup", d.Ratio)
	}
	if d.PctChange > -20 || d.PctChange < -23 {
		t.Errorf("PctChange = %v, want about -21.7", d.PctChange)
	}
	if d.NBase != 5 || d.NCand != 5 {
		t.Errorf("N = %d/%d, want 5/5", d.NBase, d.NCand)
	}
}

func TestCompareIdenticalSamplesIsNotSignificant(t *testing.T) {
	base := setOf(t, "BenchmarkX", 100, 101, 99, 100, 102)
	cand := setOf(t, "BenchmarkX", 100, 101, 99, 100, 102)
	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if d.Significant {
		t.Errorf("Significant = true for identical samples (P=%v)", d.P)
	}
}

func TestCompareMissingBenchmark(t *testing.T) {
	base := setOf(t, "BenchmarkX", 1, 2)
	cand := NewSet()
	if _, err := Compare(base, cand, "BenchmarkX", UnitTime); err == nil {
		t.Fatal("Compare() = nil error, want error for missing candidate data")
	}
}

func TestCompareZeroBaselineIsAnError(t *testing.T) {
	base := setOf(t, "BenchmarkX", 0, 0)
	cand := setOf(t, "BenchmarkX", 1, 1)
	if _, err := Compare(base, cand, "BenchmarkX", UnitTime); err == nil {
		t.Fatal("Compare() = nil error, want error for zero baseline center")
	}
}

func TestGeoMean(t *testing.T) {
	// 0.5 and 2.0 cancel exactly; geomean is 1.
	got, err := GeoMean([]Delta{{Ratio: 0.5}, {Ratio: 2.0}})
	if err != nil {
		t.Fatalf("GeoMean: %v", err)
	}
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("GeoMean = %v, want 1", got)
	}
}

func TestGeoMeanAllFaster(t *testing.T) {
	got, err := GeoMean([]Delta{{Ratio: 0.8}, {Ratio: 0.8}})
	if err != nil {
		t.Fatalf("GeoMean: %v", err)
	}
	if math.Abs(got-0.8) > 1e-9 {
		t.Errorf("GeoMean = %v, want 0.8", got)
	}
}

func TestGeoMeanEmptyIsAnError(t *testing.T) {
	if _, err := GeoMean(nil); err == nil {
		t.Fatal("GeoMean(nil) = nil error, want error")
	}
}

func TestCompareAllErrorsWhenBenchmarkDisappears(t *testing.T) {
	base := NewSet()
	base.record("BenchmarkA", UnitTime, 10)
	base.record("BenchmarkA", UnitTime, 11)
	base.record("BenchmarkGone", UnitTime, 5)
	base.record("BenchmarkGone", UnitTime, 5)

	cand := NewSet()
	cand.record("BenchmarkA", UnitTime, 9)
	cand.record("BenchmarkA", UnitTime, 9)

	ds, err := CompareAll(base, cand, UnitTime)
	if err == nil {
		t.Fatalf("CompareAll() = nil error, want error for missing BenchmarkGone")
	}
	if ds != nil {
		t.Errorf("CompareAll returned non-nil slice on error: %+v", ds)
	}
	if !contains(err.Error(), "BenchmarkGone") {
		t.Errorf("error message does not mention BenchmarkGone: %v", err)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCompareAllSortedByName(t *testing.T) {
	base := NewSet()
	// Insert in non-alphabetical order: C, A, B
	base.record("BenchmarkC", UnitTime, 10)
	base.record("BenchmarkC", UnitTime, 11)
	base.record("BenchmarkA", UnitTime, 20)
	base.record("BenchmarkA", UnitTime, 21)
	base.record("BenchmarkB", UnitTime, 30)
	base.record("BenchmarkB", UnitTime, 31)

	cand := NewSet()
	// Insert in different order: B, C, A
	cand.record("BenchmarkB", UnitTime, 9)
	cand.record("BenchmarkB", UnitTime, 9)
	cand.record("BenchmarkC", UnitTime, 19)
	cand.record("BenchmarkC", UnitTime, 19)
	cand.record("BenchmarkA", UnitTime, 29)
	cand.record("BenchmarkA", UnitTime, 29)

	ds, err := CompareAll(base, cand, UnitTime)
	if err != nil {
		t.Fatalf("CompareAll: %v", err)
	}
	if len(ds) != 3 {
		t.Fatalf("CompareAll returned %d deltas, want 3", len(ds))
	}
	if ds[0].Name != "BenchmarkA" || ds[1].Name != "BenchmarkB" || ds[2].Name != "BenchmarkC" {
		t.Errorf("Names not in alphabetical order: %s, %s, %s", ds[0].Name, ds[1].Name, ds[2].Name)
	}
}

// benchmath attaches warnings to its Summary and Comparison results and its
// documentation says they "should be reported to the user". They are the only
// signal that a comparison is underpowered — that the numbers printed next to
// them cannot support the conclusion someone is about to draw — so Compare
// must carry them out rather than drop them on the floor.

func TestCompareSurfacesTooFewSamplesForAConfidenceInterval(t *testing.T) {
	// At 95% confidence the median's interval needs >= 6 observations; with
	// 5 it is ±∞ and benchmath says so on the Summary.
	base := setOf(t, "BenchmarkX", 412.3, 401.1, 409.9, 415.0, 407.2)
	cand := setOf(t, "BenchmarkX", 321.0, 318.4, 325.9, 319.1, 322.7)

	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(d.Warnings) == 0 {
		t.Fatal("Warnings is empty; benchmath warned that 5 samples give no finite confidence interval")
	}
	if !hasSubstring(d.Warnings, "confidence interval") {
		t.Errorf("Warnings = %q, want one naming the confidence interval", d.Warnings)
	}
}

func TestCompareSurfacesTooFewSamplesToDetectADifference(t *testing.T) {
	// The Mann-Whitney U test cannot produce p < 0.05 with 3 observations
	// per side however far apart they are; benchmath warns on the Comparison.
	base := setOf(t, "BenchmarkX", 400, 401, 402)
	cand := setOf(t, "BenchmarkX", 100, 101, 102)

	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !hasSubstring(d.Warnings, "detect a difference") {
		t.Errorf("Warnings = %q, want one saying more samples are needed to detect a difference", d.Warnings)
	}
}

func TestCompareReportsNoWarningsWhenWellPowered(t *testing.T) {
	base := setOf(t, "BenchmarkX", 412.3, 401.1, 409.9, 415.0, 407.2, 410.4, 405.8, 413.1)
	cand := setOf(t, "BenchmarkX", 321.0, 318.4, 325.9, 319.1, 322.7, 320.2, 323.8, 317.6)

	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none for 8 well-separated observations per side", d.Warnings)
	}
}

func TestCompareDoesNotRepeatTheSameWarningPerSide(t *testing.T) {
	// Baseline and candidate summaries produce the identical "need >= 6
	// samples" text; printing it twice per benchmark is noise.
	base := setOf(t, "BenchmarkX", 412.3, 401.1, 409.9, 415.0, 407.2)
	cand := setOf(t, "BenchmarkX", 321.0, 318.4, 325.9, 319.1, 322.7)

	d, err := Compare(base, cand, "BenchmarkX", UnitTime)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	seen := map[string]bool{}
	for _, w := range d.Warnings {
		if seen[w] {
			t.Errorf("Warnings repeats %q: %q", w, d.Warnings)
		}
		seen[w] = true
	}
}

func hasSubstring(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
