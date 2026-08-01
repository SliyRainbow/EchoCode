//go:build !windows

package workspacegit

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
