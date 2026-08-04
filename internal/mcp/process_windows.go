//go:build windows

package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func mcpCommand(executable string, args ...string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(executable)))
	var command *exec.Cmd
	if extension == ".cmd" || extension == ".bat" {
		commandProcessor := strings.TrimSpace(os.Getenv("COMSPEC"))
		if commandProcessor == "" {
			commandProcessor = "cmd.exe"
		}
		commandArgs := append([]string{"/d", "/c", executable}, args...)
		command = exec.Command(commandProcessor, commandArgs...)
	} else {
		command = exec.Command(executable, args...)
	}
	command.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return command
}
