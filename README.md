# autoresearch-go

**Autonomous AI-driven performance optimization for any Go repository.**

Point your coding agent at your repo and go to sleep. It proposes an optimization,
runs it through a frozen measurement harness, and the harness decides: **KEEP** or
**DISCARD**. You wake up to a log of experiments and faster code.

Inspired by [karpathy/autoresearch](https://github.com/karpathy/autoresearch), which
does this for a single-GPU LLM training loop. This does it for Go — where the metric
is `ns/op` instead of `val_bpb`, and where **correctness is not optional**.

> **Status: early development.** The measurement core is being built task by task.
> The benchmark numbers in this README are real measurements from the toolchain, not
> illustrations — see [Worked example](#worked-example).

---

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
| `profile` | Runs the declared benchmarks under Go's CPU and memory profilers and prints the top hot spots — real `pprof` data on where time and allocations actually go, rather than an agent guessing from reading source. Writes `.autoresearch/profiles/{cpu,mem}.out`, openable with `go tool pprof -http=: <file>`. |
| `eval` | Runs one experiment: gates (scope, config integrity, restore, build, vet, test), measures the candidate against the pinned baseline, scores it, appends a `results.tsv` row, and exits `0`/`1`/`2`/`3` for KEEP/DISCARD/FAIL/CRASH. |
| `report` | Summarizes `results.tsv`: counts by status, cumulative speedup across kept experiments, and the largest individual wins. |

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

The commit stays and the branch advances. Had the change been slower, broken a test, or
been indistinguishable from noise, the harness would have returned `DISCARD` or `FAIL`
and the agent would `git reset --hard`.

## What the harness enforces

An agent optimizing your code can "win" by cheating. Each route is closed:

| Cheat | Why it fails |
|---|---|
| Weaken or delete a test | `_test.go` files are hashed at baseline and **restored** before every run — edits are erased, not argued about |
| Delete the work the benchmark measures | the frozen tests still run and still assert the real behavior |
| Add an easier benchmark | any `_test.go` absent from the frozen manifest is rejected |
| Edit files outside the agreed area | `scope` violations fail before anything is even built |
| Bank measurement noise as a win | a Mann-Whitney test must clear `p < 0.05`; noise is `DISCARD` |
| Speed up A by wrecking B | any significant regression over 5 % rejects the change outright |
| Swap or edit a dependency (`go.mod`/`go.sum`) | rejected outright regardless of `scope` — a dependency change is a human decision, not an autonomous one, and would change what is being measured rather than how fast it runs |
| Loosen the rules mid-run (raise `max_regress_pct`, narrow `scope`, drop a benchmark) | `.autoresearch/config.yaml` is hashed at baseline; any change to it fails the run with a config-hash mismatch |
| Compare against a stale baseline | the baseline is **re-measured every run**, interleaved with the candidate |

That last one matters more than it looks. Comparing a candidate measured now against a
baseline measured an hour ago on a cooler CPU attributes thermal drift to your code
change. Alternating both sides in one session cancels it.

## Scoring

One number, so nothing can be cherry-picked:

```
score = geomean(new_ns / base_ns)   across the declared benchmark set
```

`KEEP` requires **all** of: score < 1, at least one statistically significant
improvement, and no significant regression beyond `max_regress_pct` (default 5 %).
`allocs/op` and `B/op` are measured and shown to the agent as hints, but never scored.

## Limitations

Stated plainly, because performance tools that oversell are worse than useless:

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
