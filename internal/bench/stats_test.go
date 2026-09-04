package bench

import (
	"math"
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

func TestCompareAllOnlyCommonBenchmarks(t *testing.T) {
	base := NewSet()
	base.record("BenchmarkA", UnitTime, 10)
	base.record("BenchmarkA", UnitTime, 11)
	base.record("BenchmarkGone", UnitTime, 5)
	base.record("BenchmarkGone", UnitTime, 5)

	cand := NewSet()
	cand.record("BenchmarkA", UnitTime, 9)
	cand.record("BenchmarkA", UnitTime, 9)

	ds, err := CompareAll(base, cand, UnitTime)
	if err != nil {
		t.Fatalf("CompareAll: %v", err)
	}
	if len(ds) != 1 || ds[0].Name != "BenchmarkA" {
		t.Fatalf("CompareAll = %+v, want only BenchmarkA", ds)
	}
}
