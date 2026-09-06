// Command autor3search-go is a frozen measurement harness that lets an AI
// coding agent autonomously optimize a Go repository.
//
// The binary never edits source code. It gates correctness, measures
// candidate against baseline, and returns a verdict.
package main

import (
	"fmt"
	"os"
)

// Exit codes. Callers (and program.md) branch on these.
const (
	exitOK      = 0 // KEEP
	exitDiscard = 1 // DISCARD
	exitFail    = 2 // FAIL: scope, vet, or test gate
	exitCrash   = 3 // CRASH: build failure or timeout
	exitUsage   = 64
)

// command is one subcommand. args excludes the subcommand name itself.
type command struct {
	summary string
	run     func(args []string) int
}

// commands is the subcommand registry.
var commands = map[string]command{
	"init":     {"scan repo, discover benchmarks, write config and program.md", runInit},
	"doctor":   {"check whether this machine can measure reliably", runDoctor},
	"baseline": {"create the run branch, freeze tests, record the baseline", runBaseline},
	"profile":  {"profile the declared benchmarks and report hot spots", runProfile},
	"eval":     {"run one experiment step and return a verdict", runEval},
	"status":   {"show where the run is: branch, worktree, experiments, stop state", runStatus},
	"stop":     {"ask the agent to end the run after the current experiment", runStop},
	"report":   {"summarize results.tsv", runReport},
	"version":  {"print which build of the harness this is", runVersion},
}

func usage() {
	fmt.Fprintln(os.Stderr, "autor3search-go — autonomous Go performance optimization harness")
	fmt.Fprintln(os.Stderr, "\nusage: autor3search-go <command> [flags]\n\ncommands:")
	for _, name := range []string{"init", "doctor", "baseline", "profile", "eval", "status", "stop", "report", "version"} {
		fmt.Fprintf(os.Stderr, "  %-9s %s\n", name, commands[name].summary)
	}
}

func dispatch(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return exitUsage
	}
	return cmd.run(args[1:])
}

func main() { os.Exit(dispatch(os.Args[1:])) }
