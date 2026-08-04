//go:build !windows

package extensions

import (
	"context"
	"os/exec"
)

func extensionCommandContext(ctx context.Context, executable string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, args...)
}
