//go:build !windows

package mcp

import "os/exec"

func mcpCommand(executable string, args ...string) *exec.Cmd {
	return exec.Command(executable, args...)
}
