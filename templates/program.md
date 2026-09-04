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
  `results.tsv` and move on; do not try to route around it.
- Edit anything outside `scope`, including `.autoresearch/`, `go.mod`, and
  `go.sum`. The scope gate fails the experiment before it is even measured.
- Try to weaken, disable, or reinterpret the verdict. `eval`'s exit code and
  `--json` output are the only truth. If a result looks wrong, say so in
  `results.tsv`; do not try to make the harness agree with you by other
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

With `-json`, `eval` prints one JSON object to stdout with (at least) these
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

Append one row to `results.tsv` after every experiment, KEEP or not. Create
it with a header row if it does not already exist:

```
timestamp	commit	status	score	idea	note
```

- `timestamp` — RFC 3339, when the experiment finished.
- `commit` — the short hash of the commit you just evaluated (before any
  `git reset --hard` on a non-KEEP).
- `status` — `KEEP`, `DISCARD`, `FAIL`, or `CRASH`, copied from the verdict.
- `score` — the score from the verdict JSON, or blank if the gate failed
  before scoring.
- `idea` — a short slug for what you tried, e.g. `preallocate-map`.
- `note` — one sentence: what you expected, what happened, what you'd try
  next if this comes up again.

This file is the human's morning read. Keep rows terse — one line each — and
never delete a row, including for discards. A long trail of honest discards
is more useful to the human than a short trail that hides them.

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

LOOP FOREVER:

1. Check git state: confirm you are on the run branch.
2. If you have no strong hypothesis, run `autoresearch-go profile` and read the hot spots.
3. Change ONE thing in the in-scope Go source. One idea per experiment.
4. `git add -A && git commit -m "<idea>"`
5. `autoresearch-go eval --json > run.log 2>&1` (redirect everything; do NOT tee, it floods your context)
6. Read the verdict: `grep '"status"' run.log`
7. KEEP  -> leave the commit in place, the branch advances.
   Anything else -> `git reset --hard HEAD~1`
8. Append a row to `results.tsv` describing what you tried and what happened.
9. Go to 1.

**NEVER STOP**: Do not pause to ask whether to continue. The human may be
asleep. You are autonomous. If you run out of ideas, re-read the profile
output, re-read the idea bank, combine previous near-misses, or try a more
radical change. The loop runs until you are interrupted.
