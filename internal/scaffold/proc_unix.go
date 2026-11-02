//go:build unix

package scaffold

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own and makes
// cancelling kill the whole group, so a stream run through a shell
// (sh -c "tail -f … | jq -c .") stops with its children.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
