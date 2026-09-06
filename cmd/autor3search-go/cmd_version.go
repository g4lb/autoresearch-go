package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// runVersion reports which build of the harness is running.
//
// Worth having because a results.tsv row is only as reproducible as the
// binary that produced it: "which version measured this" is otherwise
// unanswerable from an installed binary.
func runVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	// ok is false only for a binary built without module support, which
	// formatBuildVersion reports as unknown rather than refusing to run.
	info, _ := debug.ReadBuildInfo()
	fmt.Println(formatBuildVersion(info))
	return exitOK
}

// formatBuildVersion renders the build identity recorded in info.
//
// `go install ...@latest` stamps Main.Version with the module version. A
// `go build` from a source checkout leaves it "(devel)" and records the
// commit in the VCS settings instead, so the commit is reported there — a
// bare "(devel)" identifies nothing.
func formatBuildVersion(info *debug.BuildInfo) string {
	if info == nil {
		return "autor3search-go (version unknown)"
	}

	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}

	version := info.Main.Version
	if version == "" || version == "(devel)" {
		version = checkoutVersion(settings)
	}

	lines := []string{"autor3search-go " + version}
	if platform := buildPlatform(settings); platform != "" && info.GoVersion != "" {
		lines = append(lines, fmt.Sprintf("built with %s, %s", info.GoVersion, platform))
	}
	return strings.Join(lines, "\n")
}

// checkoutVersion names a build that carries no module version by its commit.
func checkoutVersion(settings map[string]string) string {
	rev := settings["vcs.revision"]
	if rev == "" {
		return "(devel)"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	// A dirty build is called out because the commit no longer describes
	// what was built, so a measurement it produced cannot be reproduced
	// from that commit alone.
	if settings["vcs.modified"] == "true" {
		return fmt.Sprintf("(devel, %s, dirty)", rev)
	}
	return fmt.Sprintf("(devel, %s)", rev)
}

// buildPlatform is the GOOS/GOARCH the binary was built for, or "" when the
// build did not record it.
func buildPlatform(settings map[string]string) string {
	goos, goarch := settings["GOOS"], settings["GOARCH"]
	if goos == "" || goarch == "" {
		return ""
	}
	return goos + "/" + goarch
}
