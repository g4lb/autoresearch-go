package demo

import (
	"reflect"
	"strings"
	"testing"
)

func TestCountWords(t *testing.T) {
	got := CountWords("the quick brown the")
	want := map[string]int{"the": 2, "quick": 1, "brown": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CountWords = %v, want %v", got, want)
	}
}

func TestCountWordsEmpty(t *testing.T) {
	if got := CountWords("   "); len(got) != 0 {
		t.Fatalf("CountWords = %v, want empty", got)
	}
}

func TestCountWordsPunctuation(t *testing.T) {
	got := CountWords("Hello, WORLD! hello?")
	want := map[string]int{"hello": 2, "world": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CountWords = %v, want %v", got, want)
	}
}

var benchInput = strings.Repeat("The Quick, Brown Fox! jumps over 2 lazy dogs. ", 200)

func BenchmarkCountWords(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Consume the result so the compiler cannot eliminate the call.
		if len(CountWords(benchInput)) == 0 {
			b.Fatal("empty result")
		}
	}
}
