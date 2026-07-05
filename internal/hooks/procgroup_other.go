//go:build !unix

package hooks

import "os/exec"

// setProcessGroup is a no-op on non-unix platforms; exec.CommandContext's
// default cancellation (Process.Kill) applies. Hooks are a unix-oriented
// shell-command feature, so process-group teardown is only wired there.
func setProcessGroup(cmd *exec.Cmd) {}
