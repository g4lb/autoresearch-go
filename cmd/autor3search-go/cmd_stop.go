package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/state"
)

// defaultForceGrace is how long `stop -force` waits for a signalled eval to
// tear down its benchmark subprocesses before escalating. It is generous on
// purpose: the polite signal is the one that lets eval cancel its own
// context, which is what kills the whole `go test` process group. Escalating
// early would leave the grandchild benchmark binary running — the exact
// outcome force-stop exists to prevent.
const defaultForceGrace = 10 * time.Second

// exitPollInterval is how often forceStop re-checks whether the signalled
// eval has let go of the run.
const exitPollInterval = 50 * time.Millisecond

// runStop asks a run to end.
//
// Two speeds, and the difference is who decides when to stop:
//
//   - Plain `stop` writes a request the AGENT reads at its next verdict. The
//     experiment under way finishes and is scored, its KEEP or DISCARD is
//     applied, and only then does the loop exit. Nothing is thrown away.
//   - `stop -force` additionally signals the running eval to abandon the
//     experiment now. That is for when the human cannot wait for a long
//     benchmark to finish.
//
// Both leave the repository on the run branch with every kept commit intact.
func runStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	tag := fs.String("tag", "", "run tag, when the current branch is not the run branch")
	clear := fs.Bool("clear", false, "cancel a pending stop request so the loop may continue")
	force := fs.Bool("force", false, "also signal the running eval to abandon the current experiment")
	grace := fs.Duration("grace", defaultForceGrace, "with -force, how long to let the running eval shut down before killing its process group")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *clear && *force {
		fmt.Fprintln(os.Stderr, "autor3search-go stop: -clear and -force are opposites; pass one or neither")
		return exitUsage
	}

	ref, code := resolveRunRef("stop", *dir, *tag)
	if code != exitOK {
		return code
	}

	if *clear {
		if err := state.ClearStop(ref.StateDir); err != nil {
			fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
			return exitUsage
		}
		fmt.Printf("stop request cleared for run %q\n", ref.Tag)
		fmt.Println("the agent will keep experimenting; run `autor3search-go stop` again to stop it")
		return exitOK
	}

	if err := state.RequestStop(ref.StateDir); err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
		return exitUsage
	}
	fmt.Printf("stop requested for run %q\n", ref.Tag)

	if !*force {
		fmt.Println("the agent will finish the experiment it is running and then exit the loop")
		fmt.Println("to cancel:      autor3search-go stop -clear")
		fmt.Println("to stop sooner: autor3search-go stop -force")
		return exitOK
	}
	return forceStop(ref, *grace)
}

// forceStop signals the running eval and reports the state it leaves behind.
// The stop request has already been written by the caller, so an agent that
// survives the signal still sees the stop at its next verdict.
func forceStop(ref runRef, grace time.Duration) int {
	pid, running, err := state.EvalRunning(ref.StateDir)
	if err != nil {
		// A corrupt pid file is reported, not guessed at — see
		// state.EvalRunning. The stop request still stands.
		fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
		fmt.Fprintln(os.Stderr, "the stop request was written; the agent will still stop at its next verdict")
		return exitUsage
	}

	if !running {
		// Either nothing was in flight, or an eval died without releasing
		// its claim. Clearing the leftover keeps `status` honest.
		if err := state.ClearEvalPID(ref.StateDir); err != nil {
			fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
			return exitUsage
		}
		fmt.Println("no eval running — nothing to signal")
		printRepoState(ref)
		return exitOK
	}

	fmt.Printf("signalling eval (pid %d)...\n", pid)
	if err := termEval(pid); err != nil {
		fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
		return exitUsage
	}
	killed := false
	if !waitForRelease(ref.StateDir, grace) {
		fmt.Printf("eval did not exit within %s; killing its process group\n", grace)
		if err := killEvalGroup(pid); err != nil {
			fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
			return exitUsage
		}
		if !waitForRelease(ref.StateDir, grace) {
			fmt.Fprintf(os.Stderr, "autor3search-go stop: eval (pid %d) still holds the run after SIGKILL\n", pid)
			return exitUsage
		}
		killed = true
	}
	fmt.Println("eval exited; its benchmark subprocesses were torn down with it")

	// Only tidy up after a SIGKILL, which runs none of eval's own cleanup.
	// An eval that shut down politely removed its pid file itself, and
	// deleting unconditionally here would race a NEXT eval that had already
	// claimed the run: it would hold a lock on an unlinked file, leaving
	// `status` reporting an idle loop and `stop -force` with nothing to
	// signal — the brake failing precisely when it is reached for.
	if killed {
		if err := state.ClearEvalPID(ref.StateDir); err != nil {
			fmt.Fprintf(os.Stderr, "autor3search-go stop: %v\n", err)
			return exitUsage
		}
	}

	printRepoState(ref)
	return exitOK
}

// waitForRelease polls until no live process holds the run's claim, or the
// grace period runs out.
//
// It asks state.EvalRunning rather than probing the pid, because the pid
// answers the wrong question. A signalled process that has died but not yet
// been reaped by its parent — the agent's shell — is a ZOMBIE, and kill(pid,
// 0) against a zombie still succeeds: polling the pid would wait out the
// full grace period and then escalate to SIGKILL against a process that was
// already dead. The kernel releases the claim's flock the moment the process
// dies, zombie or not, so the claim is the honest liveness signal.
func waitForRelease(stateDir string, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		if _, running, err := state.EvalRunning(stateDir); err != nil || !running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(exitPollInterval)
	}
}

// printRepoState tells the human what a forced stop left behind. It never
// changes the repository: dropping a commit is the human's call, and an
// experiment abandoned mid-flight may still be worth keeping by hand.
func printRepoState(ref runRef) {
	fmt.Println()
	fmt.Println("repository state:")
	fmt.Printf("  %-10s %s\n", "branch", ref.Branch)

	commit, err := gitx.HeadCommit(ref.Root)
	if err != nil {
		return
	}
	subject, err := gitx.HeadSubject(ref.Root)
	if err != nil {
		return
	}
	fmt.Printf("  %-10s %s %q\n", "HEAD", commit, subject)
	fmt.Println()
	fmt.Println("if the agent had already committed the experiment it was running, that commit")
	fmt.Println("carries no verdict. To drop it:  git reset --hard HEAD~1")
	fmt.Println("to resume this run later:        autor3search-go stop -clear")
}
