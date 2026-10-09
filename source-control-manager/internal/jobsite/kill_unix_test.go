//go:build unix

package jobsite

import (
	"os/exec"
	"syscall"
	"testing"
)

func assertProcessKilled(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.ProcessState == nil {
		t.Fatalf("expected command with non-nil ProcessState")
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		if !ws.Signaled() {
			t.Fatalf("expected process to be terminated by signal, exited normally with status %d", ws.ExitStatus())
		}
		if ws.Signal() != syscall.SIGKILL {
			t.Fatalf("expected SIGKILL, got %v", ws.Signal())
		}
	}
}
