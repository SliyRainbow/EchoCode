//go:build windows

package tools

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow 让 Windows 上执行命令时不弹出 cmd 控制台黑框。
// CREATE_NO_WINDOW = 0x08000000。
func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
