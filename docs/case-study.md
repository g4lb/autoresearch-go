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

Six experiments: 3 kept, 2 discarded, 1 crash — but see the correction
below: one of those keeps was wrong, and finding out why produced the most
important fix in the project.

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

Running against a real library immediately exposed three bugs that seventeen
tasks of review had not, because both require conditions the demo fixture
does not have:

1. **The keep rule stopped discriminating after the first win** — the flaw
   described in the correction above, and the most serious of the three.

2. **`profile` was broken on any multi-package repository.** `go test
   -cpuprofile` refuses to run across multiple packages. The demo fixture has
   one package; `go-humanize` has two. Fixed: `profile` now profiles only the
   packages that declare the benchmarks being measured.

3. **`report` overstated cumulative improvement by roughly 80%.** It
   multiplied the kept experiments' scores — but every evaluation measures
   against the *same* pinned baseline, so each score already includes the
   improvements before it. On this run it claimed 71.9% where the truth was
   40.2%. Fixed: the cumulative figure is the most recent kept score.

Both are recorded here rather than quietly patched out, because a tool that
asks you to trust its numbers should show what it got wrong.

## Correction: this run was measured under a flawed keep rule

The run above was performed with v0.1.0, whose keep rule was wrong in a way
this very log exposes.

Every evaluation compared the candidate against a baseline pinned to the
ORIGINAL commit, which never moved. A change was kept when the score was
below 1 and at least one benchmark had significantly improved. Once `Commaf`
and `Bytes` were optimized, both conditions were permanently satisfied by
those earlier wins — so any later change that merely failed to regress was
kept, whether or not it did anything.

The last row above is the evidence: `re-measure kept state` changed no code
at all and was recorded as `keep`. Continuing the session afterwards, a
commit that added a single comment was also kept, at score 0.597.

So of the three keeps here, **two are genuine and one is spurious**. The two
real optimizations stand — each showed its own benchmark improving
significantly, verified against the library's frozen tests — but the count
and the third row do not.

**Fixed in v0.1.1** by advancing the measurement baseline: after a keep, the
pinned worktree moves to the newly kept commit, so each evaluation asks "did
THIS change help?" rather than "is the current state better than where we
started?". The frozen tests deliberately do NOT advance — they stay anchored
to the original commit, because a run whose success criteria drift is a run
that proves nothing.

Re-running the same scenario under v0.1.1, the no-op that used to be kept:

```
BenchmarkCommaf-10         -0.5%  [p=1.000 n=10]  (not significant)
BenchmarkCommas-10         -0.2%  [p=0.684 n=10]  (not significant)
SCORE  1.001  (+0.1%)
VERDICT: DISCARD
```

This is left in rather than quietly rewritten because it is the most useful
thing the run produced. Six directed experiments looked like enough to
validate the loop; they were not. The flaw only appears once several
experiments have accumulated, which is exactly the condition an overnight run
creates and a short demonstration does not.

## Two more libraries, under v0.1.1

The run above used v0.1.0. After the keep rule was fixed, the tool was run
against two further real libraries to see whether the fixes held and whether
more real-repo bugs surfaced.

### github.com/mitchellh/mapstructure @ `8508981`

Reflection-heavy decoding, four core `Decode` benchmarks, 5 test files
frozen. Three experiments, three keeps, no discards.

| Benchmark | before | after | allocations |
|---|---:|---:|---|
| `Decode` | 1254 ns/op | 989.2 ns/op | 38 → 28 |
| `DecodeBasic` | 4597 ns/op | 2705 ns/op | **93 → 52** |
| `DecodeMap` | 842.1 ns/op | 721.3 ns/op | 28 → 23 |
| `DecodeSlice` | 646.8 ns/op | 523.2 ns/op | 21 → 16 |

**Cumulative: −25.2%.**

The three changes, all in `decodeStructFromMap`, all found from the profile:

1. `dataValKeys` was a `map[reflect.Value]struct{}` that is only ever ranged
   over, never indexed — paying `reflect.Value` hashing for nothing when
   `MapKeys()` already returns a slice. Dropped, and the remaining
   `dataValKeysUnused` map presized.
2. Struct tags were parsed with `strings.Split`, allocating a slice for every
   field of every struct on every decode. Replaced with `strings.IndexByte`
   scanning. This is where most of the allocation reduction came from.
3. The field slice was grown from empty despite `NumField()` being known, and
   an error slice was allocated eagerly even when there were no errors.

**This run also validated the v0.1.1 arithmetic against reality.** `report`
computed the cumulative figure as the product of the kept scores
(0.857 × 0.917 × 0.951 = 0.748, i.e. −25.2%). Measuring the original commit
against the final state directly gives a geomean of 0.753 — within half a
percentage point. The advancing-baseline design and the product arithmetic
agree with independently measured ground truth.

### github.com/google/uuid @ `2d3c2a9`

Chosen as the honest-negative test: already tightly optimised, with `Parse`
at 11.79 ns/op and **zero allocations**. The expectation was that the tool
would correctly find nothing.

That expectation was wrong. One change was kept:

```
BenchmarkUUID_String-10       -17.7%  [p=0.000 n=10]
BenchmarkUUID_MarshalJSON-10   -5.5%  [p=0.001 n=10]
BenchmarkParse-10              +0.3%  (not significant)
BenchmarkNew-10                +0.1%  (not significant)
```

`encodeHex` made five calls to `hex.Encode`; replacing them with a
table-driven inline encoder made `String` measurably faster. Direct
measurement of the original against the final state confirms it: 18.78 →
15.30 ns/op, about −18.5%, against the harness's −17.7%.

Two honest notes on this one. First, an earlier attempt crashed on an unused
import — the same mistake made on `go-humanize`, caught the same way, which
says something about what an agent actually gets wrong. Second, the −5.5% on
`MarshalJSON` could not be confirmed by a quick direct check, which showed no
difference. That check was three sequential runs per side taken minutes
apart — precisely the methodology the interleaved harness exists to replace —
so for an effect that small the harness's ten interleaved rounds are the more
trustworthy measurement, not the spot check.

### What the two extra runs did not find

No new bugs in the tool. The three found by the first run — the keep rule,
`profile` on multi-package repositories, and `report`'s arithmetic — were the
crop, and the fixes held across two further libraries with different shapes.

## Caveats

- Six experiments on one machine. A single run on a single library is a
  demonstration, not evidence about the tool's general yield.
- macOS measurement is noisier than Linux; see the README's limitations.
- These changes were not submitted upstream to `go-humanize`. They are
  offered here as measured results, not as a patch anyone has reviewed for
  merge.
