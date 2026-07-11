//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the hook command in its own process group and, on context
// cancellation, SIGKILLs the whole group. A hook is typically a shell that forks
// its own children (e.g. `bash -c "curl ..."`); killing only the shell would
// leave those grandchildren running as orphans holding the command's stdout/
// stderr pipes, which makes Wait block until they exit — defeating the bounded
// drain on shutdown. Killing the group makes the drain timeout actually bound.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

// setServiceProcessGroup puts a long-running service in its own process group
// without wiring CommandContext cancellation — services are drained via
// terminateProcessGroup (SIGTERM → grace → SIGKILL) instead.
func setServiceProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup delivers sig to the process group led by pid.
// Negative pid targets the group (unix convention).
func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}

// terminateProcessGroup sends SIGTERM to the child's process group. Used as the
// polite first step of bounded service drain.
func terminateProcessGroup(pid int) error {
	return signalProcessGroup(pid, syscall.SIGTERM)
}

// killProcessGroup force-kills the child's process group with SIGKILL.
func killProcessGroup(pid int) error {
	return signalProcessGroup(pid, syscall.SIGKILL)
}
