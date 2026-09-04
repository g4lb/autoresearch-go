package main

import (
	"fmt"
	"os"
)

func notImplemented(name string) int {
	fmt.Fprintf(os.Stderr, "autoresearch-go %s: not implemented yet\n", name)
	return exitUsage
}

func runInit(args []string) int     { return notImplemented("init") }
func runDoctor(args []string) int   { return notImplemented("doctor") }
func runBaseline(args []string) int { return notImplemented("baseline") }
func runProfile(args []string) int  { return notImplemented("profile") }
func runEval(args []string) int     { return notImplemented("eval") }
func runReport(args []string) int   { return notImplemented("report") }
