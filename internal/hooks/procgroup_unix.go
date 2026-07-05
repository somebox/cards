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
			// Negative pid targets the process group led by the child.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}
