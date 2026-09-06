package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionReportsTheModuleVersionOfAReleasedBuild(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "v0.2.4"},
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
		},
	}

	got := formatBuildVersion(info)
	want := "autoresearch-go v0.2.4\nbuilt with go1.26.5, darwin/arm64"
	if got != want {
		t.Errorf("formatBuildVersion() =\n%s\nwant\n%s", got, want)
	}
}

func TestVersionReportsTheCommitOfABuildFromASourceCheckout(t *testing.T) {
	// `go build` in a checkout leaves Main.Version as "(devel)": there is no
	// module version to report, so the commit is the only identity the
	// binary has.
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1aaaf4b6f46a7cb4b576662bedfa112d64a8c914"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
		},
	}

	got := formatBuildVersion(info)
	want := "autoresearch-go (devel, 1aaaf4b)\nbuilt with go1.26.5, linux/amd64"
	if got != want {
		t.Errorf("formatBuildVersion() =\n%s\nwant\n%s", got, want)
	}
}

func TestVersionMarksABuildFromAModifiedCheckoutAsDirty(t *testing.T) {
	// A dirty build is worth calling out: the commit no longer identifies
	// what was measured, so a results.tsv row produced by it is not
	// reproducible from that commit alone.
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1aaaf4b6f46a7cb4b576662bedfa112d64a8c914"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
		},
	}

	got := formatBuildVersion(info)
	if !strings.Contains(got, "1aaaf4b, dirty") {
		t.Errorf("formatBuildVersion() = %q, want it to mark the build dirty", got)
	}
}

func TestVersionPassesAPseudoVersionThroughVerbatim(t *testing.T) {
	// What a `go build` from a checkout actually produces under Go 1.24+:
	// Main.Version holds a pseudo-version (commit and dirty flag included)
	// rather than "(devel)", so the "(devel)" path above never fires here.
	// It is reported unchanged on purpose — this is the string `go version
	// -m` prints, and the one worth pasting into a bug report.
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "v0.2.5-0.20260906165138-0d7ff5a952a1+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
		},
	}

	got := formatBuildVersion(info)
	want := "autoresearch-go v0.2.5-0.20260906165138-0d7ff5a952a1+dirty\nbuilt with go1.26.5, darwin/arm64"
	if got != want {
		t.Errorf("formatBuildVersion() =\n%s\nwant\n%s", got, want)
	}
}

func TestVersionSurvivesBuildInfoBeingUnavailable(t *testing.T) {
	// debug.ReadBuildInfo reports ok=false for a binary built without module
	// support. Reporting "unknown" beats panicking on a nil pointer.
	got := formatBuildVersion(nil)
	want := "autoresearch-go (version unknown)"
	if got != want {
		t.Errorf("formatBuildVersion(nil) = %q, want %q", got, want)
	}
}

func TestVersionOmitsThePlatformLineWhenTheBuildDoesNotRecordIt(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v0.2.4"}}

	got := formatBuildVersion(info)
	want := "autoresearch-go v0.2.4"
	if got != want {
		t.Errorf("formatBuildVersion() = %q, want %q", got, want)
	}
}

func TestRunVersionPrintsToStdoutAndSucceeds(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = runVersion(nil) })

	if code != exitOK {
		t.Errorf("runVersion() = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "autoresearch-go ") {
		t.Errorf("runVersion() printed %q, want it to start with the binary name", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("runVersion() printed %q, want a trailing newline", out)
	}
}

func TestRunVersionRejectsAnUnknownFlag(t *testing.T) {
	captureStderr(t, func() {
		if code := runVersion([]string{"-nope"}); code != exitUsage {
			t.Errorf("runVersion(-nope) = %d, want %d", code, exitUsage)
		}
	})
}

func TestVersionIsRegisteredAsACommand(t *testing.T) {
	if _, ok := commands["version"]; !ok {
		t.Error("command \"version\" not registered")
	}
}
