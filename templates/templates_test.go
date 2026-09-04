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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
