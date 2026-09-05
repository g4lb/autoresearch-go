# autoresearch-go

**Autonomous AI-driven performance optimization for any Go repository.**

Point your coding agent at your repo and go to sleep. It proposes an optimization,
runs it through a frozen measurement harness, and the harness decides: **KEEP** or
**DISCARD**. You wake up to a log of experiments and faster code.

Inspired by [karpathy/autoresearch](https://github.com/karpathy/autoresearch), which
does this for a single-GPU LLM training loop. This does it for Go — where the metric
is `ns/op` instead of `val_bpb`, and where **correctness is not optional**.

> **Status: v0.1.1, early but working.** Validated against three real libraries —
> `go-humanize`, `mapstructure` and `google/uuid` — with measured wins in each. Every
> number in this README and in [the case study](docs/case-study.md) is a real
> measurement, never an illustration. The case study also records the bugs those runs
> found in the tool itself.

---

## Start here

Open your coding agent inside the Go repository you want to make faster, and
paste this:

```text
Install and run autoresearch-go on this repository, then optimize it.

Setup:
1. go install github.com/g4lb/autoresearch-go/cmd/autoresearch-go@latest
   Make sure $(go env GOPATH)/bin is on PATH.
2. autoresearch-go init
   Show me the benchmarks it discovered. If it reports none, STOP and tell me:
   this tool can only optimize what it can measure.
3. git add -A && git commit -m "autoresearch-go init"
4. autoresearch-go doctor
   Show me any warnings. If the machine looks unfit to measure, stop and ask me
   before continuing.
5. autoresearch-go baseline -tag <today, e.g. sep6>

Then:
6. Read program.md in this repository, in full. It is your instruction set for
   the rest of this run. Follow it exactly.

Rules for the whole run:
- Never edit program.md, .autoresearch/config.yaml, results.tsv, or anything
  the harness writes. They are not yours.
- Never pass -force to any autoresearch-go command.
- One idea per experiment. Commit before each eval.
- KEEP means the commit stays. Anything else means git reset --hard HEAD~1.

Run the loop until I stop you.
```

That's the whole handoff. The agent installs the tool, sets the run up, and then
follows `program.md` — which the harness generated for your repository and which
tells it how to run the keep-or-discard loop.

What you get back: one commit per accepted change on a branch named
`autoresearch-go/<tag>`, and a `results.tsv` recording every experiment that was
tried, including the ones that failed. `autoresearch-go report` summarizes it.

Two things worth knowing before you start it:

- **It needs benchmarks.** The tool optimizes what it can measure, and refuses to
  guess. See [Repos with no benchmarks](#repos-with-no-benchmarks).
- **Numbers are only as good as the machine.** Run `doctor` and read it. A
  thermally throttled laptop on battery produces noise dressed as data.

## The idea

You do not edit Go files to tune performance. You edit `program.md` — the instructions
that drive your agent. The agent edits the Go. A compiled harness holds the metric, and
the agent cannot reach it.

| Piece | What it is | Who edits it |
|---|---|---|
| `autoresearch-go` | the harness binary: gates, measures, scores | nobody — it's compiled |
| your `_test.go` files | frozen at baseline, restored before every run | nobody — restored automatically |
| your Go source | whatever is in `scope` | **the agent** |
| `program.md` | the agent's instructions | **you** |
| frozen tests, baseline worktree, baseline record | lives outside your repo, under `os.UserCacheDir()` | nobody — the agent could not reach it even by editing every file in scope |

That last row matters: the agent edits the repository, so anything the score depends
on that *lived* there would be silently writable by the very agent it is meant to
constrain. The only harness output that stays inside your repo is `results.tsv` (a
human-readable log, not part of the metric) and `run.log` (subprocess transcripts) —
both gitignored by `init`.

## Quick start

```bash
go install github.com/g4lb/autoresearch-go/cmd/autoresearch-go@latest

cd your-go-project
autoresearch-go init                                 # find benchmarks, write config + program.md
git add -A && git commit -m "autoresearch-go init"   # baseline refuses a dirty tree
autoresearch-go doctor                               # is this machine fit to measure?
autoresearch-go baseline -tag sep4                   # freeze tests, pin the baseline commit
```

That commit matters — `init` only writes files, it does not commit them, and
`baseline` refuses to run against an uncommitted tree because a baseline pinned
against what's on disk (not what's in git) would not be reproducible. `init` writes
three things: `.gitignore` entries, `.autoresearch/config.yaml`, and `program.md`.
`.autoresearch/config.yaml` is the one file under `.autoresearch/` that gets
committed — it's the run configuration, and humans own it; everything else the
harness later writes under `.autoresearch/` (e.g. `profile`'s pprof output) is
gitignored.

Then start your agent in the repo:

```
Read program.md and start the optimization loop.
```

It runs until you stop it. Each experiment is one commit, one verdict, one row in
`results.tsv`.

## Commands

| Command | What it does |
|---|---|
| `init` | Scans the repo, discovers benchmarks via `go/ast`, and writes `.autoresearch/config.yaml` + `program.md`. Refuses to overwrite an existing config without `-force`. |
| `doctor` | Checks whether this machine can measure reliably (CPU frequency scaling, thermal throttling risk, disk space) and prints its findings. Informational — always exits 0. |
| `baseline -tag <tag>` | Creates the run branch `autoresearch-go/<tag>`, freezes every in-scope `_test.go` file, and pins a detached worktree at the baseline commit. Refuses a dirty tree and a reused tag. |
| `profile` | Runs the declared benchmarks under Go's CPU and memory profilers and prints the top hot spots — real `pprof` data on where time and allocations actually go, rather than an agent guessing from reading source. Profiles only the package(s) that declare the benchmarks in scope (the go tool refuses `-cpuprofile`/`-memprofile` against `./...` once more than one package matches). When they're all in one package — the common case — writes `.autoresearch/profiles/{cpu,mem}.out`, openable with `go tool pprof -http=: <file>`; when benchmarks span multiple packages, profiles each in turn and writes `.autoresearch/profiles/<package>/{cpu,mem}.out`, with output grouped and labelled by package. |
| `eval` | Runs one experiment: gates (scope, config integrity, restore, build, vet, test), measures the candidate against the pinned baseline worktree, scores it, appends a `results.tsv` row, exits `0`/`1`/`2`/`3` for KEEP/DISCARD/FAIL/CRASH, and on `KEEP` re-points the pinned worktree at the candidate's commit so the next `eval` measures against it (see [Scoring](#scoring)). |
| `report` | Summarizes `results.tsv`: counts by status, the cumulative speedup as the product of every kept experiment's score, and the largest individual wins. |

Every command accepts `-C <dir>` to run against a repository other than the current
directory, rather than changing the process's working directory — safer under
concurrent invocations, and testable without `os.Chdir`.

## Worked example

A word counter. Ordinary Go, with ordinary tests.

**`wordcount.go`** — the code the agent is allowed to change:

```go
package demo

import "strings"

// CountWords returns how many times each lowercase word appears in s.
func CountWords(s string) map[string]int {
	counts := map[string]int{}
	for _, field := range strings.Fields(s) {
		word := ""
		for _, r := range field {
			if r >= 'A' && r <= 'Z' {
				r = r + ('a' - 'A')
			}
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				word = word + string(r) // quadratic: rebuilds the string every rune
			}
		}
		if word != "" {
			counts[word]++
		}
	}
	return counts
}
```

**`wordcount_test.go`** — the tests and the benchmark. **The agent cannot change this
file.** It is hashed at `baseline` and restored before every single evaluation:

```go
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
```

**The agent's change** — a `strings.Builder`, a byte loop instead of rune decoding, and
a preallocated map:

```go
func CountWords(s string) map[string]int {
	counts := make(map[string]int, 32)
	var b strings.Builder
	for _, field := range strings.Fields(s) {
		b.Reset()
		b.Grow(len(field))
		for i := 0; i < len(field); i++ {
			c := field[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				b.WriteByte(c)
			}
		}
		if b.Len() > 0 {
			counts[b.String()]++
		}
	}
	return counts
}
```

**The result.** Both versions run the identical frozen test file, and both pass it.
Measured with 10 interleaved rounds per side on an Apple M5, go1.26.5:

| metric | before | after | change | p |
|---|---:|---:|---:|---:|
| `ns/op` | 125,797 | 59,915 | **−52.4 %** | <0.0001 |
| `B/op` | 54,280 | 49,048 | −9.6 % | <0.0001 |
| `allocs/op` | 6,806 | 1,805 | **−73.5 %** | <0.0001 |

Score is the geometric mean of the per-benchmark time ratios: `0.4763`. Below 1 and
statistically significant, with no benchmark regressing — so the harness returns:

```
VERDICT: KEEP
```

The commit stays, the branch advances, and the measurement baseline advances with it —
the *next* experiment is measured against this commit, not against the original one. Had
the change been slower, broken a test, or been indistinguishable from noise, the harness
would have returned `DISCARD` or `FAIL` and the agent would `git reset --hard`.

## What the harness enforces

An agent optimizing your code can "win" by cheating. Each route is closed:

| Cheat | Why it fails |
|---|---|
| Weaken or delete a test | `_test.go` files are hashed at baseline and **restored** before every run — edits are erased, not argued about |
| Delete the work the benchmark measures | the frozen tests still run and still assert the real behavior |
| Add an easier benchmark | any `_test.go` absent from the frozen manifest is rejected |
| Replace a frozen `_test.go` with a symlink to a file outside the repo | `Snapshot` refuses to freeze a symlinked test file, and `Restore` refuses to write through one that appears later — both fail loudly instead of writing through the link |
| Edit files outside the agreed area | `scope` violations fail before anything is even built |
| Bank measurement noise as a win | a Mann-Whitney test must clear `p < 0.05`; noise is `DISCARD` |
| Speed up A by wrecking B | any significant regression over 5 % rejects the change outright |
| Swap or edit a dependency (`go.mod`/`go.sum`) | rejected outright regardless of `scope` — a dependency change is a human decision, not an autonomous one, and would change what is being measured rather than how fast it runs |
| Loosen the rules mid-run (raise `max_regress_pct`, narrow `scope`, drop a benchmark) | `.autoresearch/config.yaml` is hashed at baseline; any change to it fails the run with a config-hash mismatch |
| Compare against a stale baseline | the measurement baseline is **re-measured every run**, interleaved with the candidate |
| Coast to `KEEP` on an earlier improvement doing nothing new | the measurement baseline **advances to the newly kept commit after every `KEEP`** (see [Scoring](#scoring)), so a later no-op is compared against what was just kept, not against where the run started |

That last one matters more than it looks. Comparing a candidate measured now against a
baseline measured an hour ago on a cooler CPU attributes thermal drift to your code
change. Alternating both sides in one session cancels it.

## Scoring

One number, so nothing can be cherry-picked:

```
score = geomean(new_ns / base_ns)   across the declared benchmark set
```

`KEEP` requires **all** of:

1. **A minimum real improvement.** `score` must be below `1 - min_effect_pct/100`
   (default `min_effect_pct: 1.0`, i.e. score < 0.99), not merely below 1. A change
   that is technically significant but trivially small is not worth a commit in an
   unattended overnight loop.
2. **A Bonferroni-corrected significant improvement.** At least one benchmark's
   p-value must clear `alpha / k`, where `k` is the number of benchmarks compared
   in that experiment — not the raw `alpha` (0.05). Testing `k` benchmarks against
   the same uncorrected `alpha` inflates the chance that at least one shows a
   spurious "significant" result purely by chance (with 4 benchmarks, about an
   18% chance); dividing `alpha` by `k` is the standard correction for that.
3. **No significant regression beyond `max_regress_pct`** (default 5%). This guard
   deliberately uses the raw, **uncorrected** `alpha`, not the Bonferroni-corrected
   one from rule 2 — on purpose, and it looks inconsistent until you see why: the
   correction in rule 2 only ever makes it *harder* to call a result significant,
   and applying it to the regression guard would make real regressions *easier* to
   miss. We want the opposite bias for harm: conservative about accepting a win,
   liberal about catching damage.

A `Delta`'s reported significance (`p < alpha`, no correction) is always the raw,
honest statistic — that's what a human or agent should see when reading a report.
The Bonferroni correction in rule 2 is a KEEP-decision threshold layered on top,
not a redefinition of "significant"; `autoresearch-go eval`'s output calls out a
benchmark that is significant at `alpha` but did not clear the corrected bar,
rather than silently calling it "not significant."

`allocs/op` and `B/op` are measured and shown to the agent as hints, but never scored.

`base_ns` is **not** fixed for the whole run. `baseline` pins two things that are
kept deliberately separate: a FROZEN commit that the frozen tests and the scope
gate always compare against (so an agent cannot expand what it may edit by
banking experiments), and a MEASUREMENT commit — what `base_ns` is actually
measured against — that starts equal to the frozen one and **advances to the
candidate's own commit after every `KEEP`**. So `score` always answers "did
*this* experiment help, compared to the last thing that was kept," never "is
the tree better than when the run started." Without this, once one real
improvement was kept, every later experiment — however useless — kept
comparing against that same stale starting point and a no-op could coast to
`KEEP` on an earlier win it did not contribute to.

One consequence: each kept `score` is now only that experiment's own
incremental contribution, so `autoresearch-go report`'s cumulative speedup
is the **product** of every kept score, not the latest one alone —
successive real improvements compound the way percentage changes do.

## Limitations

Stated plainly, because performance tools that oversell are worse than useless:

- **A `KEEP` is evidence, not proof.** Any fixed significance threshold admits some
  false positives by construction — that's what "alpha" means. Measuring 100
  genuine no-op trials (a commit that changes only a comment) against the
  pre-Bonferroni rule produced 2-3 spurious `KEEP`s, roughly a 3-5% rate, consistent
  with running several benchmarks per experiment against `alpha = 0.05` each. The
  minimum-effect floor and the Bonferroni correction described in
  [Scoring](#scoring) make the rule substantially stricter — the family-wise
  correction directly targets the multiple-comparisons inflation, and the effect
  floor throws out wins too small to matter even when real — but they reduce the
  false-`KEEP` rate, they do not (and cannot) eliminate it. Treat a single `KEEP`
  as evidence worth banking, not as proof the change works.
- **Laptops are noisy.** macOS P/E core scheduling makes numbers jump. Interleaving and
  `-count` mitigate it and `doctor` warns you, but a quiet Linux box gives cleaner
  results.
- **No benchmarks, no value.** This optimizes what it can measure. `init` tells you
  plainly rather than pretending — see [Repos with no benchmarks](#repos-with-no-benchmarks).
- **A small measurement asymmetry remains.** Each round measures the baseline a
  moment before the candidate, so on a machine that is steadily warming up, the
  candidate is consistently sampled a fraction hotter. Interleaving cancels the
  drift *between* rounds, which dominates; this sub-second offset does not
  cancel. It is small next to benchmark variance, but it is a known asymmetry
  rather than an absent one.
- **Microbenchmarks are not your application.** A 50 % win on a hot function may be
  invisible end to end. Benchmark what actually matters.
- **`count` below 4 can never reach significance.** The Mann-Whitney test behind `p`
  has a best-case two-sided p-value of 0.1 at 3 measured rounds per side — above the
  0.05 threshold no matter how large or how clean the real improvement is. Every
  experiment would be discarded on a technicality, not on its merits. `config.yaml`
  refuses `count` below 4 rather than let that happen silently.
- **Measurement overhead has diminishing returns below `benchtime: 1s`.** Each
  measured round pays roughly 380 ms of `go test` process startup on top of the
  configured `benchtime`, measured on this machine, and a default experiment runs
  22 rounds (baseline + candidate, interleaved). At `benchtime: 1s` that overhead is
  about 27 % of a round's wall time; drop to `50ms` and it rises to about 70 %. Tuning
  `benchtime` down to make a run finish faster mostly buys back fixed per-round
  startup cost, not measurement time, and a shorter `benchtime` also means fewer
  benchmark iterations per round to average over — so past a point it trades run
  speed for noisier numbers rather than a proportionally faster run.

### Repos with no benchmarks

`autoresearch-go init` discovers benchmarks by scanning the repository for Go test
files and looking for functions matching `func BenchmarkXxx(b *testing.B)`. If it
finds none, it refuses to write `.autoresearch/config.yaml` and exits with an error,
rather than generating a config with an empty `benchmarks:` list that would silently
optimize nothing.

That refusal is deliberate: `autoresearch-go` has no other notion of "faster." The
verdict — `KEEP`, `DISCARD`, `FAIL`, `CRASH` — is entirely a function of the declared
benchmarks' timings across a baseline and a candidate. No benchmarks means no signal
to gate on, at which point every candidate would either be rejected for no reason or
accepted for no reason.

To use `autoresearch-go` on a repository like this:

1. Write at least one benchmark that covers the code you actually want made faster —
   a plain Go benchmark, in a `_test.go` file, of the form:

   ```go
   func BenchmarkThing(b *testing.B) {
       for i := 0; i < b.N; i++ {
           Thing()
       }
   }
   ```

2. Benchmark the right thing. A benchmark that exercises a cold path, a trivial
   helper, or a function nobody calls under load produces numbers that are entirely
   real and entirely useless — confident percentages attached to work that was never
   the bottleneck. Benchmark the function, loop, or request path that actually
   dominates the workload you care about, ideally informed by a profile of the real
   program rather than a guess.
3. Re-run `autoresearch-go init` once the benchmark exists. It will pick it up and
   proceed normally.

## License

MIT © 2026 Gal Be
