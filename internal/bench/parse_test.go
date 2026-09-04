package bench

import (
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseFixture(t *testing.T) {
	f, err := os.Open("testdata/sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	set, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := set.Names(), []string{"BenchmarkEncode-10", "BenchmarkParse-10"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v (sorted)", got, want)
	}

	// benchfmt normalizes ns/op to sec/op, so 412.3 ns is 4.123e-07 s.
	secs, ok := set.Values("BenchmarkParse-10", UnitTime)
	if !ok {
		t.Fatal("no sec/op for BenchmarkParse-10")
	}
	if len(secs) != 2 {
		t.Fatalf("sec/op = %v, want 2 observations", secs)
	}
	for i, wantNs := range []float64{412.3, 401.1} {
		if got := NsPerOp(secs[i]); math.Abs(got-wantNs) > 1e-6 {
			t.Errorf("NsPerOp(secs[%d]) = %v, want %v", i, got, wantNs)
		}
	}

	if _, ok := set.Values("BenchmarkParse-10", "ns/op"); ok {
		t.Error(`Values(..., "ns/op") succeeded; benchfmt normalizes it to sec/op`)
	}

	allocs, ok := set.Values("BenchmarkEncode-10", UnitAllocs)
	if !ok || len(allocs) != 1 || allocs[0] != 9 {
		t.Errorf("allocs = %v %v, want [9]", allocs, ok)
	}
}

func TestParseIgnoresNonBenchmarkLines(t *testing.T) {
	in := "some build noise\nPASS\nok  \texample.com/x\t0.1s\n"
	set, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(set.Names()) != 0 {
		t.Errorf("Names() = %v, want empty", set.Names())
	}
}

func TestBaseNameStripsSuffixAndSubBenchmark(t *testing.T) {
	set, err := Parse(strings.NewReader("BenchmarkParse/big-10\t100\t5 ns/op\n"))
	if err != nil {
		t.Fatal(err)
	}
	ser := set.Series["BenchmarkParse/big-10"]
	if ser == nil {
		t.Fatalf("Names() = %v, want BenchmarkParse/big-10", set.Names())
	}
	if ser.Base != "BenchmarkParse" {
		t.Errorf("Base = %q, want BenchmarkParse (prefix restored by Parse)", ser.Base)
	}
}

func TestSelectByBase(t *testing.T) {
	set, _ := Parse(strings.NewReader(
		"BenchmarkParse/big-10\t100\t5 ns/op\nBenchmarkOther-10\t100\t7 ns/op\n"))
	got := set.SelectByBase([]string{"BenchmarkParse"}).Names()
	if !reflect.DeepEqual(got, []string{"BenchmarkParse/big-10"}) {
		t.Errorf("SelectByBase = %v, want [BenchmarkParse/big-10]", got)
	}
	if n := len(set.SelectByBase(nil).Names()); n != 2 {
		t.Errorf("SelectByBase(nil) selected %d, want all 2", n)
	}
}

func TestAddMergesRounds(t *testing.T) {
	a, _ := Parse(strings.NewReader("BenchmarkX-8\t100\t10 ns/op\n"))
	b, _ := Parse(strings.NewReader("BenchmarkX-8\t100\t12 ns/op\n"))
	a.Add(b)
	got, _ := a.Values("BenchmarkX-8", UnitTime)
	// Values are seconds after benchfmt normalization: 10 ns is 1e-08 s.
	want := []float64{10e-9, 12e-9}
	if len(got) != 2 || math.Abs(got[0]-want[0]) > 1e-18 || math.Abs(got[1]-want[1]) > 1e-18 {
		t.Errorf("merged = %v, want %v", got, want)
	}
}
