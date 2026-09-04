//go:build unix

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

// setupProcessGroup puts the child in its own process group and makes
// context cancellation kill the entire group.
//
// go test execs the compiled test binary as a grandchild. Killing only the
// direct child leaves that binary running: exec.CommandContext's default
// cancel signals one pid, not the group. A leaked benchmark process would
// keep consuming CPU and corrupt every later measurement on this machine.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid signals the whole process group.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil &&
			!errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
