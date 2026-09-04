# program.md

This file is your instructions, coding agent. Read it fully before doing
anything. It is the only thing a human edits to steer this run — everything
else (the metric, the gates, the verdict) is a compiled binary you cannot
change and should not try to.

## Setup

Before starting the loop, do this once:

1. Agree a run tag with the human if one was not already given to you (a
   short slug like `sep4` — today's date or similar is fine).
2. Confirm `autoresearch-go baseline -tag <tag>` has already been run for
   this tag. If it has not, stop and ask the human to run it, or run it
   yourself if you have been told you may. `baseline` creates the run
   branch, freezes the current test files as the golden copies, and records
   the commit this run measures against. Everything downstream depends on
   this step having happened exactly once.
3. Read the repository. Skim the package(s) named in `scope` in
   `.autoresearch/config.yaml`. Run the declared benchmarks yourself once
   (`autoresearch-go profile`) so you know what you are starting from before
   changing anything.

## Experimentation

What you MAY do:

- Edit any Go source file that falls under one of the `scope` patterns in
  `.autoresearch/config.yaml`.
- Add new files inside `scope`, as long as they are not `_test.go` files
  that duplicate or replace existing tests.
- Add a *new* benchmark inside `scope` — it will not count toward the score
  (the score only covers the declared `benchmarks` list) but it can help you
  reason about a hot path.
- Run any read-only diagnostic command (`go vet`, `go build -gcflags='-m'`,
  `autoresearch-go profile`) as often as you like between experiments.

What you MUST NOT do:

- Edit any `_test.go` file. It is restored from the frozen baseline copy
  before every `eval`, so an edit there is silently discarded — worse, it
  wastes an experiment slot for nothing. If a test looks wrong, say so in
  your `-desc` for that experiment and move on; do not try to route around
  it.
- Edit `go.mod` or `go.sum`, ever. These are rejected outright regardless of
  what `scope` says. Changing a dependency is a supply-chain decision a
  human makes, not something an unattended loop decides, and a swapped
  dependency can change *what* is measured, not just how fast it runs.
- Edit `.autoresearch/config.yaml`. It is not covered by the scope gate —
  the gate only sees ordinary source files, and this file is gitignored so
  it is invisible to it either way. Instead, its hash is recorded at
  `baseline` time; if it has changed by `eval` time, the run fails with
  reason `config_changed`, not `scope_violation`.
- Edit an ordinary source file outside `scope`. This *is* what the scope
  gate itself rejects, failing the experiment before it is even measured.
- Try to weaken, disable, or reinterpret the verdict. `eval`'s exit code and
  `--json` output are the only truth. If a result looks wrong, say so in
  your `-desc`; do not try to make the harness agree with you by other
  means.
- Batch multiple unrelated changes into one experiment. One idea per
  experiment keeps every result attributable and every discard cheap.

## Output format

`autoresearch-go eval` is the only command whose result decides anything.
It exits with one of four codes, and program.md's loop below branches on
exactly these:

| Exit code | Meaning | Verdict status |
|---|---|---|
| `0` | KEEP  — the change is a real, safe improvement | `KEEP` |
| `1` | DISCARD — no significant improvement, or it lost the coin flip against noise | `DISCARD` |
| `2` | FAIL — a gate rejected the change: scope violation, a new/edited test file, `go vet` failure, or a test failure | `FAIL` |
| `3` | CRASH — the build failed outright or a phase timed out | `CRASH` |

Any status the harness cannot classify is reported as exit code `2`
(FAIL) rather than a silent success — treat an unrecognized `--json` status
the same way you would treat FAIL.

With `--json`, `eval` prints one JSON object to stdout with (at least) these
fields:

```json
{
  "status": "KEEP",
  "reason": "improved",
  "score": 0.9123,
  "message": "score 0.9123 (-8.77%)",
  "regressions": []
}
```

`status` is one of `KEEP`, `DISCARD`, `FAIL`, `CRASH`. `reason` is a stable,
machine-readable code (e.g. `improved`, `no_significant_improvement`,
`guard_regression`, `scope_violation`, `new_test_file`, `build_failed`,
`vet_failed`, `tests_failed`, `timeout`). `score` is the geometric mean of
`new_ns/base_ns` across the declared benchmarks — below 1 is faster.
`message` is a human-readable one-liner suitable for a log row.
`regressions`, when present, lists which benchmarks tripped the regression
guard and by how much.

## Logging

`results.tsv` is HARNESS-OWNED. `eval` appends exactly one row to it,
automatically, on every invocation, KEEP or not. You must never create,
append to, or edit `results.tsv` yourself — any manual write is either
redundant (the row already exists) or corrupts a file the harness parses
strictly, which fails the human's morning `autoresearch-go report`, or a
future `autoresearch-go baseline -force`, on the whole file, not just your
line.

The columns the human sees are:

```
commit	score	best_bench_delta	allocs_delta	status	description
```

`commit`, `score`, `best_bench_delta`, `allocs_delta`, and `status` are
filled in by the harness from the verdict — you do not control them. The one
column that is yours is `description`, and you set it by passing `-desc` to
`eval`:

```
autoresearch-go eval --json -desc "preallocate the map"
```

Always pass `-desc`, on every invocation, KEEP or not — it is the only
record of what you were trying. Keep it terse — a short slug, e.g.
`preallocate-map` — and never dishonest: describe what you actually tried,
including for a DISCARD, FAIL, or CRASH. This file is the human's morning
read. A long trail of honest discards is more useful to the human than a
short trail that hides them.

## Go optimization idea bank

Measure first, then reach for these:

- **Allocations.** `go build -gcflags='-m' ./...` shows what escapes to the heap.
  Escaped values in a hot loop are the most common easy win.
- **Preallocate.** `make([]T, 0, n)` when n is known. Same for maps.
- **Reuse buffers.** `sync.Pool`, or a caller-supplied `[]byte` scratch buffer.
- **strings.Builder** instead of `+=` in a loop. Repeated concatenation is quadratic.
- **Avoid interface boxing.** Passing a concrete type where `any` is expected
  allocates. Generics or concrete signatures avoid it.
- **Bounds-check elimination.** Hoist `_ = s[n-1]` or slice once before a loop.
- **Struct field alignment.** Order fields large to small to shrink the struct.
- **map vs slice.** For small, fixed key sets a linear scan over a slice beats
  a map. Measure at your real size.
- **Algorithmic change.** Usually the biggest win. Ask whether the work is
  needed at all before micro-optimizing it.
- **Byte vs rune.** Ranging a string decodes UTF-8; indexing bytes does not.
  Only valid when the data is genuinely ASCII.

## The experiment loop

`eval` already splits its own output for you: the verdict — one compact
JSON object — is written to stdout, and the full, noisy build/vet/test/
benchmark transcript is written automatically to `run.log` in the
repository root. `run.log` is the harness's own file, opened by `eval`
itself before it does anything else — never redirect `eval`'s stdout into
it yourself (`eval --json > run.log 2>&1` or similar). Doing so opens a
second, independent descriptor on a path the harness already has open for
the transcript; if the two ever fall out of step, whichever one writes
second overwrites the other from byte 0, destroying exactly the
diagnostic transcript you need when a step FAILs or CRASHes and you want
to see why. Run `eval --json` bare and read the verdict straight from its
stdout; open `run.log` only to read it, never to write to it.

LOOP FOREVER:

1. Check git state: confirm you are on the run branch.
2. If you have no strong hypothesis, run `autoresearch-go profile` and read the hot spots.
3. Change ONE thing in the in-scope Go source. One idea per experiment.
4. `git add -A && git commit -m "<idea>"`
5. `autoresearch-go eval --json -desc "<idea>"` (no redirect — do NOT tee or
   send stdout to `run.log`; either floods your context or clobbers the
   transcript, see above). `-desc` is what lands in `results.tsv`'s
   `description` column for this experiment — always pass it.
6. Read the verdict directly from stdout: it is one compact JSON object,
   so parse or `grep '"status"'` it straight from the command's own output.
7. KEEP  -> leave the commit in place, the branch advances.
   Anything else -> `git reset --hard HEAD~1`
8. Go to 1.

**NEVER STOP**: Do not pause to ask whether to continue. The human may be
asleep. You are autonomous. If you run out of ideas, re-read the profile
output, re-read the idea bank, combine previous near-misses, or try a more
radical change. The loop runs until you are interrupted.
