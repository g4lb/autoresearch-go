# Case study: optimizing github.com/dustin/go-humanize

A real run of `autoresearch-go` against a real, maintained, third-party Go
library. Every number here was measured; nothing is illustrative.

## Setup

| | |
|---|---|
| Target | [`github.com/dustin/go-humanize`](https://github.com/dustin/go-humanize) @ `4d1d908` |
| Machine | Apple M5, 10 logical cores, macOS |
| Toolchain | go1.26.5 darwin/arm64 |
| Benchmarks declared | `BenchmarkCommas`, `BenchmarkCommaf`, `BenchmarkBytes`, `BenchmarkBigBytes` |
| Test files frozen | 12 |
| `count` | 10 interleaved rounds per side |
| `benchtime` | 300ms |
| `-race` on the correctness gate | on (default) |

The agent driving the loop was an LLM following `program.md`, which is the
intended usage. This was a directed session of six experiments, not an
unattended overnight run of a hundred — so treat it as a demonstration that
the loop works end to end on real code, not as a claim about what a full
night produces.

## Result

Two genuine optimizations were found, verified against the library's own
frozen test suite, and kept:

| Function | Before | After | Allocations |
|---|---:|---:|---|
| `Commaf` | 92.33 ns/op | 44.84 ns/op | 4 → 1 |
| `Bytes` | 124.0 ns/op | 37.43 ns/op | 4 → 1 |

`BigBytes` (129.1 → 134.6 ns/op) and `Commas` (17.31 → 18.31 ns/op) were
unchanged within noise.

**Cumulative: −40.2%** across the declared benchmark set, geomean.

### What the changes were

`Commaf` built its output with a `bytes.Buffer` and used `strings.Split` to
locate the decimal point — allocating a string slice purely to find one
byte. Replaced with `strings.IndexByte` and a single pre-sized `[]byte`.

`humanateBytes` called `fmt.Sprintf` **to build a format string**, then passed
that to a second `fmt.Sprintf`. Replaced with `strconv.AppendFloat` into a
pre-sized buffer.

Neither change alters behavior: the library's own tests are frozen at
baseline and restored before every evaluation, so they ran unmodified
against every candidate.

## The full log

```
commit   score   best_delta  allocs  status    description
a98aacb  0.9962  -0.98        0.00   discard   baseline sanity check, no code change
35b424a  0.0000   0.00        0.00   crash     Commaf: single preallocated buffer (broken: unused import)
02cafde  0.7936 -57.00      -75.00   keep      Commaf: single preallocated buffer, no strings.Split
46d7246  0.5917 -71.20      -75.00   keep      humanateBytes: strconv.AppendFloat, drop the double Sprintf
c01f261  0.5337 -71.45      -75.00   discard   humanateBigBytes: AppendFloat instead of Sprintf
46d7246  0.5981 -70.69      -75.00   keep      re-measure kept state
```

Six experiments: 3 kept, 2 discarded, 1 crash. Every gate fired at least once.

## The interesting one: the regression guard earning its keep

Experiment 5 applied the same `AppendFloat` treatment to `humanateBigBytes`.
It worked — `BigBytes` got 37.8% faster, allocations fell 7 → 5, and the
overall score improved to **0.534, a headline −46.6%**.

It was rejected:

```
BenchmarkBigBytes-10      -37.8%  [p=0.000 n=10]
BenchmarkBytes-10         -71.4%  [p=0.000 n=10]
BenchmarkCommaf-10        -57.3%  [p=0.000 n=10]
BenchmarkCommas-10         +7.0%  [p=0.000 n=10]

SCORE  0.534  (-46.6%)   guard: max regress +7.0% < 5.0% TRIPPED
VERDICT: DISCARD
```

`Commas` — an unrelated function the change never touched — regressed 7.0%
at p=0.000, over the 5% guard.

That could have been measurement noise, so it was checked: re-measuring the
kept state (without the `BigBytes` change) put `Commas` back at **+0.1%,
p=0.781, not significant**. The regression was real and attributable to that
change — most plausibly a code-layout effect from pulling `strconv` into
`bigbytes.go`.

Without the guard, the loop would have banked a "46.6% improvement" while
making an unrelated function measurably slower. This is the single clearest
demonstration of why the harness scores a whole benchmark set rather than
one number, and why a significant regression rejects a change however good
its headline looks.

## What this run found in the tool itself

Running against a real library immediately exposed two bugs that seventeen
tasks of review had not, because both require conditions the demo fixture
does not have:

1. **`profile` was broken on any multi-package repository.** `go test
   -cpuprofile` refuses to run across multiple packages. The demo fixture has
   one package; `go-humanize` has two. Fixed: `profile` now profiles only the
   packages that declare the benchmarks being measured.

2. **`report` overstated cumulative improvement by roughly 80%.** It
   multiplied the kept experiments' scores — but every evaluation measures
   against the *same* pinned baseline, so each score already includes the
   improvements before it. On this run it claimed 71.9% where the truth was
   40.2%. Fixed: the cumulative figure is the most recent kept score.

Both are recorded here rather than quietly patched out, because a tool that
asks you to trust its numbers should show what it got wrong.

## Caveats

- Six experiments on one machine. A single run on a single library is a
  demonstration, not evidence about the tool's general yield.
- macOS measurement is noisier than Linux; see the README's limitations.
- These changes were not submitted upstream to `go-humanize`. They are
  offered here as measured results, not as a patch anyone has reviewed for
  merge.
