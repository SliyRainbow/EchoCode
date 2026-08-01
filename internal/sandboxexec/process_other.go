//go:build !windows

package sandboxexec

import "os/exec"

type platformProcess struct{}

func startPlatformProcess(cmd *exec.Cmd, _ Limits) (*platformProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &platformProcess{}, nil
}

func (p *platformProcess) terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (p *platformProcess) close() error { return nil }

func platformCapabilities() Capabilities {
	return Capabilities{
		Platform: "other",
		Backend:  "process-only",
		Summary:  "Only the root process is managed on this platform.",
	}
}
