//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func shellCommand(ctx context.Context) (*exec.Cmd, string) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.CommandContext(ctx, shell, "-i"), shell
}

func configureProcess(_ *exec.Cmd) {}

func lineEnding() string { return "\n" }
