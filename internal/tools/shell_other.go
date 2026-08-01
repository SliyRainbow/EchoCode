//go:build !windows

package tools

import "os/exec"

// hideConsoleWindow 在非 Windows 平台无需处理（不会弹控制台窗口）。
func hideConsoleWindow(_ *exec.Cmd) {}
