//go:build windows

package terminal

import (
	"context"
	"os/exec"
	"syscall"
)

func shellCommand(ctx context.Context) (*exec.Cmd, string) {
	if powershell, err := exec.LookPath("powershell.exe"); err == nil {
		bootstrap := "$utf8 = New-Object System.Text.UTF8Encoding($false); " +
			"[Console]::InputEncoding = $utf8; " +
			"[Console]::OutputEncoding = $utf8; " +
			"$OutputEncoding = $utf8"
		return exec.CommandContext(ctx, powershell,
			"-NoLogo", "-NoProfile", "-NoExit", "-ExecutionPolicy", "Bypass", "-Command", bootstrap,
		), "Windows PowerShell"
	}
	return exec.CommandContext(ctx, "cmd.exe", "/Q", "/D", "/K", "chcp 65001>nul"), "Command Prompt"
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func lineEnding() string { return "\r\n" }
