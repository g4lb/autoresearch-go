package templates

import "testing"

func TestProgramMDEmbedsContent(t *testing.T) {
	b := ProgramMD()
	if len(b) == 0 {
		t.Fatal("ProgramMD() returned empty content")
	}
	want := "## The experiment loop"
	if !contains(string(b), want) {
		t.Errorf("ProgramMD() missing %q", want)
	}
}

// TestProgramMDResultsTSVOwnership guards against the regression where
// program.md told the agent to write its own rows to results.tsv, using a
// schema incompatible with the one internal/results.Load actually parses
// (eval already appends one harness-owned row per invocation; see
// cmd/autor3search-go/cmd_eval.go and internal/results/results.go). An
// agent following the old instructions corrupted results.tsv, which fails
// every later `report` or `baseline -force` on the whole file.
func TestProgramMDResultsTSVOwnership(t *testing.T) {
	s := string(ProgramMD())

	if !contains(s, "-desc") {
		t.Error(`ProgramMD() does not mention "-desc" — the agent needs to know how to ` +
			"get a description into the harness-written results.tsv row")
	}

	// The old, broken schema program.md once told the agent to write
	// itself. If this fragment is present, program.md has regressed to
	// instructing a schema internal/results cannot parse.
	brokenSchema := "timestamp\tcommit\tstatus\tscore\tidea\tnote"
	if contains(s, brokenSchema) {
		t.Errorf("ProgramMD() contains the old, harness-incompatible results.tsv schema %q", brokenSchema)
	}

	// The agent must be told, in some form, that it never writes this
	// file itself.
	if !contains(s, "never") || !contains(s, "results.tsv") {
		t.Error(`ProgramMD() should tell the agent it must never write results.tsv itself`)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestProgramMDDocumentsTheGracefulStop guards the other half of the loop's
// contract. The loop is written as "LOOP FOREVER", so the ONLY thing that
// ends it short of an interrupt is the agent noticing stop_requested in a
// verdict and acting on it. If program.md stops telling the agent that, a
// human running `autor3search-go stop` gets no response at all.
func TestProgramMDDocumentsTheGracefulStop(t *testing.T) {
	s := string(ProgramMD())

	for _, want := range []string{
		"stop_requested", // the field the agent branches on
		"autor3search-go stop",
	} {
		if !contains(s, want) {
			t.Errorf("ProgramMD() missing %q — the agent cannot honour a graceful stop without it", want)
		}
	}
}

// TestProgramMDTellsTheAgentToApplyTheVerdictBeforeStopping guards the
// "graceful" in graceful stop: a stop must not abandon the verdict of the
// experiment that just finished, or the run branch is left holding a commit
// nothing ever decided on.
func TestProgramMDTellsTheAgentToApplyTheVerdictBeforeStopping(t *testing.T) {
	s := string(ProgramMD())
	if !contains(s, "Apply this verdict") {
		t.Error("ProgramMD() does not tell the agent to apply the current verdict before leaving the loop")
	}
	if !contains(s, "Do NOT start another experiment") {
		t.Error("ProgramMD() does not tell the agent to stop starting experiments once a stop is pending")
	}
}
