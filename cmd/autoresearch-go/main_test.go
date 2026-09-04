package main

import "testing"

func TestDispatchUnknownCommand(t *testing.T) {
	code := dispatch([]string{"nope"})
	if code != exitUsage {
		t.Fatalf("dispatch(nope) = %d, want %d", code, exitUsage)
	}
}

func TestDispatchNoArgsPrintsUsage(t *testing.T) {
	code := dispatch(nil)
	if code != exitUsage {
		t.Fatalf("dispatch(nil) = %d, want %d", code, exitUsage)
	}
}

func TestKnownCommandsAreRegistered(t *testing.T) {
	want := []string{"init", "doctor", "baseline", "profile", "eval", "report"}
	for _, name := range want {
		if _, ok := commands[name]; !ok {
			t.Errorf("command %q not registered", name)
		}
	}
}
