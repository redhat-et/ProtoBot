//go:build !unix

package jobsite

import (
	"os/exec"
	"testing"
)

func assertProcessKilled(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.ProcessState == nil {
		t.Fatalf("expected command with non-nil ProcessState")
	}
	if cmd.ProcessState.Success() {
		t.Fatalf("expected process to be terminated, exited normally")
	}
}
