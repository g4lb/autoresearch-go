package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/g4lb/autor3search-go/internal/doctor"
)

// runDoctor checks whether this machine can measure reliably.
func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "repository root (or a directory inside it)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	findings := doctor.Check(*dir)

	// Print findings.
	maxSeverity := doctor.SeverityOK
	for _, f := range findings {
		emoji := "✓"
		switch f.Severity {
		case doctor.SeverityOK:
			emoji = "✓"
		case doctor.SeverityWarn:
			emoji = "⚠"
		case doctor.SeverityFail:
			emoji = "✗"
		case doctor.SeverityNotApplicable:
			emoji = "–"
		}

		fmt.Printf("%s %s: %s\n", emoji, f.Name, f.Detail)

		if f.Severity > maxSeverity && f.Severity != doctor.SeverityNotApplicable {
			maxSeverity = f.Severity
		}
	}

	// Exit with OK status (doctor is informational, not fatal).
	return exitOK
}
