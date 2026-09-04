package measure

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/bench"
)

func TestInterleaveAlternatesBaseAndCandidate(t *testing.T) {
	var order []string
	base := func(ctx context.Context, r int) (*bench.Set, error) {
		order = append(order, "base")
		return mkSet(10), nil
	}
	cand := func(ctx context.Context, r int) (*bench.Set, error) {
		order = append(order, "cand")
		return mkSet(8), nil
	}

	b, c, err := Interleave(context.Background(), 3, false, base, cand)
	if err != nil {
		t.Fatalf("Interleave: %v", err)
	}
	want := []string{"base", "cand", "base", "cand", "base", "cand"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	bv, _ := b.Values("BenchmarkX-8", bench.UnitTime)
	cv, _ := c.Values("BenchmarkX-8", bench.UnitTime)
	if len(bv) != 3 || len(cv) != 3 {
		t.Fatalf("collected %d base and %d cand observations, want 3 and 3", len(bv), len(cv))
	}
}

func TestInterleaveDiscardsWarmupRound(t *testing.T) {
	calls := 0
	base := func(ctx context.Context, r int) (*bench.Set, error) {
		calls++
		return mkSet(10), nil
	}
	cand := func(ctx context.Context, r int) (*bench.Set, error) {
		calls++
		return mkSet(9), nil
	}

	b, _, err := Interleave(context.Background(), 2, true, base, cand)
	if err != nil {
		t.Fatalf("Interleave: %v", err)
	}
	if calls != 6 {
		t.Errorf("calls = %d, want 6 (3 rounds x 2 sides, first discarded)", calls)
	}
	bv, _ := b.Values("BenchmarkX-8", bench.UnitTime)
	if len(bv) != 2 {
		t.Errorf("kept %d observations, want 2", len(bv))
	}
}

func TestInterleavePropagatesError(t *testing.T) {
	boom := errors.New("boom")
	base := func(ctx context.Context, r int) (*bench.Set, error) { return mkSet(1), nil }
	cand := func(ctx context.Context, r int) (*bench.Set, error) { return nil, boom }

	if _, _, err := Interleave(context.Background(), 2, false, base, cand); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestInterleaveRejectsTooFewRounds(t *testing.T) {
	f := func(ctx context.Context, r int) (*bench.Set, error) { return mkSet(1), nil }
	if _, _, err := Interleave(context.Background(), 1, false, f, f); err == nil {
		t.Fatal("Interleave(rounds=1) = nil error, want error")
	}
}

// mkSet builds a one-observation Set for BenchmarkX-8.
func mkSet(ns float64) *bench.Set {
	s, err := bench.Parse(strings.NewReader(
		fmt.Sprintf("BenchmarkX-8\t100\t%v ns/op\n", ns)))
	if err != nil {
		panic(err)
	}
	return s
}
