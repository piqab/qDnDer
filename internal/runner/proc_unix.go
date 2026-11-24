//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child into its own process group so the whole
// tree (shell + children) can be killed on cancellation.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills the child's whole process group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
