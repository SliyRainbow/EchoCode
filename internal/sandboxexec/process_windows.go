//go:build windows

package sandboxexec

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	jobObjectCPURateControlEnable  = 0x1
	jobObjectCPURateControlHardCap = 0x4
	saferScopeUser                 = 2
	saferLevelNormalUser           = 0x00020000
	saferLevelOpen                 = 1
	securityMandatoryMediumRID     = 0x2000
)

var (
	advapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	saferCreateLevelProc           = advapi32.NewProc("SaferCreateLevel")
	saferComputeTokenFromLevelProc = advapi32.NewProc("SaferComputeTokenFromLevel")
	saferCloseLevelProc            = advapi32.NewProc("SaferCloseLevel")
)

type jobObjectCPURateControlInformation struct {
	ControlFlags uint32
	CPURate      uint32
}

type platformProcess struct {
	mu  sync.Mutex
	job windows.Handle
}

func startPlatformProcess(cmd *exec.Cmd, limits Limits) (*platformProcess, error) {
	job, err := createJobObject(limits)
	if err != nil {
		return nil, fmt.Errorf("create Windows Job Object: %w", err)
	}
	control := &platformProcess{job: job}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	var limitedToken windows.Token
	if limits.RestrictPrivileges {
		limitedToken, err = createLimitedUserToken()
		if err != nil {
			_ = control.close()
			return nil, fmt.Errorf("create Windows limited user token: %w", err)
		}
		defer limitedToken.Close()
		cmd.SysProcAttr.Token = syscall.Token(limitedToken)
	}

	if err := cmd.Start(); err != nil {
		_ = control.close()
		return nil, err
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = control.close()
		return nil, fmt.Errorf("assign process to Windows Job Object: %w", err)
	}
	if err := resumeProcess(uint32(cmd.Process.Pid)); err != nil {
		_ = control.terminate(cmd)
		_ = cmd.Wait()
		_ = control.close()
		return nil, fmt.Errorf("resume sandboxed process: %w", err)
	}
	return control, nil
}

func createLimitedUserToken() (windows.Token, error) {
	var source windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY,
		&source,
	); err != nil {
		return 0, err
	}
	defer source.Close()

	base := source
	var linked windows.Token
	if source.IsElevated() {
		var err error
		linked, err = source.GetLinkedToken()
		if err != nil {
			return createSaferNormalUserToken()
		}
		defer linked.Close()
		base = linked
	}

	var primary windows.Token
	err := windows.DuplicateTokenEx(
		base,
		windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID,
		nil,
		windows.SecurityImpersonation,
		windows.TokenPrimary,
		&primary,
	)
	if err != nil {
		return 0, err
	}
	if err := setMediumIntegrity(primary); err != nil {
		_ = primary.Close()
		return 0, fmt.Errorf("set limited token integrity: %w", err)
	}
	return primary, nil
}

func createSaferNormalUserToken() (windows.Token, error) {
	var level windows.Handle
	result, _, callErr := saferCreateLevelProc.Call(
		saferScopeUser,
		saferLevelNormalUser,
		saferLevelOpen,
		uintptr(unsafe.Pointer(&level)),
		0,
	)
	if result == 0 {
		return 0, windowsCallError(callErr)
	}
	defer saferCloseLevelProc.Call(uintptr(level))

	var token windows.Token
	result, _, callErr = saferComputeTokenFromLevelProc.Call(
		uintptr(level),
		0,
		uintptr(unsafe.Pointer(&token)),
		0,
		0,
	)
	if result == 0 {
		return 0, windowsCallError(callErr)
	}
	if err := setMediumIntegrity(token); err != nil {
		_ = token.Close()
		return 0, fmt.Errorf("set SAFER token integrity: %w", err)
	}
	return token, nil
}

// setMediumIntegrity keeps the restricted child below the elevated integrity
// label of the parent process. The administrator SID is disabled separately;
// both controls are needed for the sandbox state shown in the UI.
func setMediumIntegrity(token windows.Token) error {
	var sid *windows.SID
	if err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_MANDATORY_LABEL_AUTHORITY,
		1,
		securityMandatoryMediumRID,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		&sid,
	); err != nil {
		return err
	}
	defer windows.FreeSid(sid)

	label := windows.Tokenmandatorylabel{
		Label: windows.SIDAndAttributes{
			Sid:        sid,
			Attributes: windows.SE_GROUP_INTEGRITY,
		},
	}
	return windows.SetTokenInformation(
		token,
		windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)),
		label.Size(),
	)
}

func windowsCallError(err error) error {
	if err == nil || err == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return err
}

func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return err
		}
	}
}

func createJobObject(limits Limits) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	if limits.MaxProcesses > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = limits.MaxProcesses
	}
	if limits.MemoryBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(limits.MemoryBytes)
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("set process-tree limits: %w", err)
	}

	if limits.CPUPercent > 0 && limits.CPUPercent < 100 {
		cpu := jobObjectCPURateControlInformation{
			ControlFlags: jobObjectCPURateControlEnable | jobObjectCPURateControlHardCap,
			CPURate:      limits.CPUPercent * 100,
		}
		if _, err := windows.SetInformationJobObject(
			job,
			windows.JobObjectCpuRateControlInformation,
			uintptr(unsafe.Pointer(&cpu)),
			uint32(unsafe.Sizeof(cpu)),
		); err != nil {
			_ = windows.CloseHandle(job)
			return 0, fmt.Errorf("set CPU limit: %w", err)
		}
	}
	return job, nil
}

func (p *platformProcess) terminate(cmd *exec.Cmd) error {
	if p == nil {
		return killRootProcess(cmd)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job == 0 {
		return killRootProcess(cmd)
	}
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return errors.Join(fmt.Errorf("terminate Windows Job Object: %w", err), killRootProcess(cmd))
	}
	return nil
}

func (p *platformProcess) close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job == 0 {
		return nil
	}
	err := windows.CloseHandle(p.job)
	p.job = 0
	return err
}

func killRootProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func platformCapabilities() Capabilities {
	return Capabilities{
		Platform:            "windows",
		Backend:             "windows-job-object",
		ProcessTree:         true,
		ResourceLimits:      true,
		PrivilegeIsolation:  true,
		FilesystemIsolation: false,
		NetworkIsolation:    false,
		Summary:             "Windows Job Object and a limited user token with a Medium integrity label enforce process cleanup, resource limits, and privilege removal; filesystem and network boundaries are policy guarded.",
	}
}
