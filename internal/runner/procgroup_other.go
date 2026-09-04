//go:build !unix

package runner

import "os/exec"

// setupProcessGroup is a no-op on platforms without process groups.
// Timeouts there kill only the direct child; see procgroup_unix.go.
func setupProcessGroup(cmd *exec.Cmd) {}
