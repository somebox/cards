//go:build !unix

package hooks

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op on non-unix platforms; exec.CommandContext's
// default cancellation (Process.Kill) applies. Hooks are a unix-oriented
// shell-command feature, so process-group teardown is only wired there.
func setProcessGroup(cmd *exec.Cmd) {}

func setServiceProcessGroup(cmd *exec.Cmd) {}

func terminateProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func killProcessGroup(pid int) error {
	return terminateProcessGroup(pid)
}
