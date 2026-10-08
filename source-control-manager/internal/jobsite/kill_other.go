//go:build !unix

package jobsite

import "os/exec"

func detachCmd(cmd *exec.Cmd) {}

func killCmdProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
