// Package bench parses Go benchmark output and compares measurement sets.
package bench

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/perf/benchfmt"
)

// Measured units, as benchfmt reports them after normalization.
//
// benchfmt rewrites the "ns/op" column to unit "sec/op" with the value in
// seconds, so UnitTime is "sec/op" and NOT "ns/op": Value("ns/op") never
// matches. Ratios are unit-independent, so scoring is unaffected; only
// human-facing output converts back to nanoseconds via NsPerOp.
const (
	UnitTime   = "sec/op"
	UnitBytes  = "B/op"
	UnitAllocs = "allocs/op"
)

// benchPrefix is the prefix benchfmt strips from parsed benchmark names and
// that Parse restores, so names match go test output and user configuration.
const benchPrefix = "Benchmark"

// NsPerOp converts a sec/op measurement to nanoseconds for display.
func NsPerOp(seconds float64) float64 { return seconds * 1e9 }

// Metric holds every observation of one unit, in observation order.
type Metric struct {
	Unit   string
	Values []float64
}

// Series holds every metric measured for a single benchmark.
type Series struct {
	// Name is the full name as reported, e.g. "BenchmarkParse/big-10".
	Name string
	// Base is the root benchmark name with sub-benchmark parts and the
	// GOMAXPROCS suffix stripped, e.g. "BenchmarkParse". Config selects on this.
	Base    string
	Metrics map[string]*Metric
}

// Set is a parsed collection of benchmark measurements, keyed by name.
type Set struct {
	Series map[string]*Series
}

// NewSet returns an empty Set.
func NewSet() *Set { return &Set{Series: map[string]*Series{}} }

// Parse reads Go benchmark format, ignoring any non-benchmark lines.
func Parse(r io.Reader) (*Set, error) {
	set := NewSet()
	rd := benchfmt.NewReader(r, "bench")
	for rd.Scan() {
		res, ok := rd.Result().(*benchfmt.Result)
		if !ok {
			// Syntax errors and file config records are not measurements.
			continue
		}
		// benchfmt strips the "Benchmark" prefix; restore it so names match
		// go test -bench output and the names users put in config.benchmarks.
		name := string(res.Name.Full())
		if !strings.HasPrefix(name, benchPrefix) {
			name = benchPrefix + name
		}
		for _, v := range res.Values {
			set.record(name, v.Unit, v.Value)
		}
	}
	if err := rd.Err(); err != nil {
		return nil, fmt.Errorf("parse benchmark output: %w", err)
	}
	return set, nil
}

func (s *Set) record(name, unit string, value float64) {
	ser, ok := s.Series[name]
	if !ok {
		base := string(benchfmt.Name(name).Base())
		if !strings.HasPrefix(base, benchPrefix) {
			base = benchPrefix + base
		}
		ser = &Series{
			Name:    name,
			Base:    base,
			Metrics: map[string]*Metric{},
		}
		s.Series[name] = ser
	}
	m, ok := ser.Metrics[unit]
	if !ok {
		m = &Metric{Unit: unit}
		ser.Metrics[unit] = m
	}
	m.Values = append(m.Values, value)
}

// Names returns every benchmark name, sorted.
func (s *Set) Names() []string {
	names := make([]string, 0, len(s.Series))
	for n := range s.Series {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Values returns the observations for one benchmark and unit.
func (s *Set) Values(name, unit string) ([]float64, bool) {
	ser, ok := s.Series[name]
	if !ok {
		return nil, false
	}
	m, ok := ser.Metrics[unit]
	if !ok {
		return nil, false
	}
	return m.Values, true
}

// Add appends every observation in other into s, preserving order.
func (s *Set) Add(other *Set) {
	for _, name := range other.Names() {
		for unit, m := range other.Series[name].Metrics {
			for _, v := range m.Values {
				s.record(name, unit, v)
			}
		}
	}
}

// SelectByBase returns the subset of s whose base names appear in bases.
// An empty bases slice selects everything. Base names let a config say
// "BenchmarkParse" and still match "BenchmarkParse/big-10".
func (s *Set) SelectByBase(bases []string) *Set {
	if len(bases) == 0 {
		return s
	}
	want := make(map[string]bool, len(bases))
	for _, b := range bases {
		want[b] = true
	}
	out := NewSet()
	for _, name := range s.Names() {
		ser := s.Series[name]
		if !want[ser.Base] {
			continue
		}
		for unit, m := range ser.Metrics {
			for _, v := range m.Values {
				out.record(name, unit, v)
			}
		}
	}
	return out
}
