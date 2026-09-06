package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/g4lb/autor3search-go/internal/results"
	"github.com/g4lb/autor3search-go/internal/state"
)

// runStatus answers "where is this run, and how do I stop it" without
// touching anything.
//
// It exists because the loop's own output cannot answer that question. The
// agent runs `eval --json`, whose contract is one JSON object and nothing
// else, so there is no human-readable channel to print a header on; and the
// human watching may not be reading the agent's transcript at all. This is
// the command they run in their own shell, at any moment, from any branch.
//
// It is read-only and always exits 0 once it has a run to describe: a human
// checking on a run must never be the reason a state file changes.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	tag := fs.String("tag", "", "run tag, when the current branch is not the run branch")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	ref, code := resolveRunRef("status", *dir, *tag)
	if code != exitOK {
		return code
	}
	base, err := state.LoadBaseline(filepath.Join(ref.StateDir, state.BaselineFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go status: %v\n", err)
		return exitUsage
	}

	runBranch := branchPrefix + ref.Tag
	fmt.Printf("%-14s %s\n", "run tag", ref.Tag)
	if ref.Branch == runBranch {
		fmt.Printf("%-14s %s  (checked out)\n", "branch", runBranch)
	} else {
		// Worth shouting about: the agent commits to the run branch, so a
		// human standing somewhere else is not looking at the run's tree.
		fmt.Printf("%-14s %s  (NOT checked out — you are on %s)\n", "branch", runBranch, ref.Branch)
	}
	fmt.Printf("%-14s %s  (run started here)\n", "baseline", base.Commit)
	printMeasuringAgainst(base)
	printWorktree(ref)
	printExperiments(ref)
	printLoopState(ref)
	printStopState(ref)
	return exitOK
}

// printMeasuringAgainst renders the ADVANCING measurement pointer, which is
// the single most misread number in a run: `score` always answers "did this
// experiment beat the last KEEP", never "is the tree better than when the
// run started". Naming how far it has moved from the frozen anchor makes
// that visible instead of implied.
func printMeasuringAgainst(base *state.Baseline) {
	if base.MeasureCommit == base.Commit {
		fmt.Printf("%-14s %s  (still the baseline — nothing kept yet)\n", "measuring vs", base.MeasureCommit)
		return
	}
	fmt.Printf("%-14s %s  (advanced past the baseline by earlier KEEPs)\n", "measuring vs", base.MeasureCommit)
}

func printWorktree(ref runRef) {
	path := ref.WorktreeDir()
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		fmt.Printf("%-14s %s  (MISSING — re-run `autor3search-go baseline -tag %s -force`)\n",
			"worktree", path, ref.Tag)
		return
	}
	fmt.Printf("%-14s %s\n", "worktree", path)
}

// printExperiments answers "how far into the loop is it" from results.tsv,
// the same file `report` reads. A missing or unreadable file is reported as
// such rather than as zero experiments: silently claiming a run has done
// nothing would be worse than admitting the count is unavailable.
func printExperiments(ref runRef) {
	rows, err := results.Load(filepath.Join(ref.Root, results.Path))
	if err != nil {
		fmt.Printf("%-14s unavailable (%v)\n", "experiments", err)
		return
	}
	if len(rows) == 0 {
		fmt.Printf("%-14s none yet\n", "experiments")
		return
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status]++
	}
	fmt.Printf("%-14s %d run  (%d keep, %d discard, %d fail, %d crash)  — next is #%d\n",
		"experiments", len(rows),
		counts["keep"], counts["discard"], counts["fail"], counts["crash"],
		len(rows)+1)
}

// printLoopState says whether an experiment is in flight right now, which is
// what tells a human whether a plain `stop` will be acted on in seconds or
// after a long benchmark finishes.
func printLoopState(ref runRef) {
	pid, running, err := state.EvalRunning(ref.StateDir)
	switch {
	case err != nil:
		fmt.Printf("%-14s unknown (%v)\n", "eval", err)
	case running:
		fmt.Printf("%-14s running (pid %d) — an experiment is being measured\n", "eval", pid)
	default:
		fmt.Printf("%-14s idle — between experiments\n", "eval")
	}
}

func printStopState(ref runRef) {
	if state.StopRequested(ref.StateDir) {
		fmt.Printf("%-14s requested — the agent will exit the loop at its next verdict\n", "stop")
		fmt.Println()
		fmt.Println("to cancel the stop:  autor3search-go stop -clear")
		fmt.Println("to stop sooner:      autor3search-go stop -force")
		return
	}
	fmt.Printf("%-14s not requested\n", "stop")
	fmt.Println()
	fmt.Println("to stop after the current experiment:  autor3search-go stop")
	fmt.Println("to stop now, abandoning it:            autor3search-go stop -force")
}
