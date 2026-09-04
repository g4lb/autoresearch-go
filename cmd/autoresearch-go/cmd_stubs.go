package main

import (
	"fmt"
	"os"
)

func notImplemented(name string) int {
	fmt.Fprintf(os.Stderr, "autoresearch-go %s: not implemented yet\n", name)
	return exitUsage
}

func runDoctor(args []string) int  { return notImplemented("doctor") }
func runProfile(args []string) int { return notImplemented("profile") }
func runReport(args []string) int  { return notImplemented("report") }
