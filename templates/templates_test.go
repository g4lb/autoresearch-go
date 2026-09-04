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
// cmd/autoresearch-go/cmd_eval.go and internal/results/results.go). An
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
