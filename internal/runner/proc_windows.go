//go:build windows

package runner

import "os/exec"

// Windows has no Unix process groups; kill the direct child only.
func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
