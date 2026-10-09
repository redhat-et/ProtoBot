//go:build unix

package jobsite

import (
	"os/exec"
	"syscall"
)

func killCmdProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	pid := cmd.Process.Pid
	if cmd.SysProcAttr != nil && (cmd.SysProcAttr.Setsid || cmd.SysProcAttr.Setpgid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}
