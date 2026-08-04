//go:build windows

package extensions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func extensionCommandContext(ctx context.Context, executable string, args ...string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(executable)))
	if extension != ".cmd" && extension != ".bat" {
		return exec.CommandContext(ctx, executable, args...)
	}
	commandProcessor := strings.TrimSpace(os.Getenv("COMSPEC"))
	if commandProcessor == "" {
		commandProcessor = "cmd.exe"
	}
	commandArgs := append([]string{"/d", "/c", executable}, args...)
	return exec.CommandContext(ctx, commandProcessor, commandArgs...)
}
