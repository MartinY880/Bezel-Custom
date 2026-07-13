//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setDetachSysProcAttr runs the command in its own session so agent
// restarts/signals don't kill an in-flight package apply.
func setDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
