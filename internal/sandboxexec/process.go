package sandboxexec

import (
	"errors"
	"os/exec"
	"sync"
)

// Limits are applied to one command or persistent terminal process tree.
// Zero values leave the corresponding resource unrestricted.
type Limits struct {
	MemoryBytes        uint64
	CPUPercent         uint32
	MaxProcesses       uint32
	RestrictPrivileges bool
}

// Capabilities describes guarantees provided by the current platform backend.
// Filesystem and network fields deliberately stay separate from command-policy
// checks so the UI never presents a text broker as an operating-system sandbox.
type Capabilities struct {
	Platform            string `json:"platform"`
	Backend             string `json:"backend"`
	ProcessTree         bool   `json:"processTree"`
	ResourceLimits      bool   `json:"resourceLimits"`
	PrivilegeIsolation  bool   `json:"privilegeIsolation"`
	FilesystemIsolation bool   `json:"filesystemIsolation"`
	NetworkIsolation    bool   `json:"networkIsolation"`
	Summary             string `json:"summary"`
}

// Process owns an exec.Cmd and the platform containment object attached to it.
// Wait must be used instead of cmd.Wait so descendants are cleaned up when the
// root process exits.
type Process struct {
	cmd      *exec.Cmd
	platform *platformProcess
	waitOnce sync.Once
	waitErr  error
}

func Start(cmd *exec.Cmd, limits Limits) (*Process, error) {
	if cmd == nil {
		return nil, errors.New("sandbox process command is nil")
	}
	platform, err := startPlatformProcess(cmd, limits)
	if err != nil {
		return nil, err
	}
	return &Process{cmd: cmd, platform: platform}, nil
}

func (p *Process) Wait() error {
	if p == nil || p.cmd == nil {
		return errors.New("sandbox process is nil")
	}
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		if closeErr := p.platform.close(); p.waitErr == nil && closeErr != nil {
			p.waitErr = closeErr
		}
	})
	return p.waitErr
}

func (p *Process) Terminate() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	return p.platform.terminate(p.cmd)
}

func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func DetectCapabilities() Capabilities {
	return platformCapabilities()
}
