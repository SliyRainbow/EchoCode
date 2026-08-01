//go:build windows

package workspacegit

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("git", "status")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("Git command must hide its Windows console window")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Git command must use CREATE_NO_WINDOW")
	}
}
