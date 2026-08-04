//go:build windows

package sandboxexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsJobObjectConfiguresResourceLimits(t *testing.T) {
	const memoryLimit = 768 * 1024 * 1024
	job, err := createJobObject(Limits{MemoryBytes: memoryLimit, CPUPercent: 75, MaxProcesses: 17})
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	wantFlags := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS)
	if info.BasicLimitInformation.LimitFlags&wantFlags != wantFlags {
		t.Fatalf("limit flags = %#x, want %#x", info.BasicLimitInformation.LimitFlags, wantFlags)
	}
	if info.BasicLimitInformation.ActiveProcessLimit != 17 {
		t.Fatalf("active process limit = %d", info.BasicLimitInformation.ActiveProcessLimit)
	}
	if info.JobMemoryLimit != memoryLimit {
		t.Fatalf("job memory limit = %d", info.JobMemoryLimit)
	}
	var cpu jobObjectCPURateControlInformation
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&cpu)),
		uint32(unsafe.Sizeof(cpu)),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if cpu.CPURate != 7500 || cpu.ControlFlags != jobObjectCPURateControlEnable|jobObjectCPURateControlHardCap {
		t.Fatalf("CPU rate = %#v", cpu)
	}
}

func TestWindowsJobObjectTerminatesRootProcess(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	process, err := Start(cmd, Limits{MemoryBytes: 512 * 1024 * 1024, MaxProcesses: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Terminate(); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err == nil {
		t.Fatal("terminated process unexpectedly exited successfully")
	}
}

func TestWindowsLimitedTokenIsAppliedToChild(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	process, err := Start(cmd, Limits{MemoryBytes: 512 * 1024 * 1024, MaxProcesses: 8, RestrictPrivileges: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Terminate()
		_ = process.Wait()
	}()

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID()))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	administratorEnabled := false
	for _, group := range groups.AllGroups() {
		if windows.EqualSid(group.Sid, administratorsSID) &&
			group.Attributes&windows.SE_GROUP_ENABLED != 0 &&
			group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 {
			administratorEnabled = true
			break
		}
	}
	if administratorEnabled {
		t.Fatal("limited child retained an enabled Administrators SID")
	}
	if rid := tokenIntegrityRID(token); rid != securityMandatoryMediumRID {
		t.Fatalf("limited child integrity RID = %#x, want %#x", rid, securityMandatoryMediumRID)
	}
}

func tokenIntegrityRID(token windows.Token) uint32 {
	var length uint32
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &length); err != windows.ERROR_INSUFFICIENT_BUFFER {
		return 0
	}
	buffer := make([]byte, length)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0], uint32(len(buffer)), &length); err != nil {
		return 0
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	if label.Label.Sid == nil || label.Label.Sid.SubAuthorityCount() == 0 {
		return 0
	}
	return label.Label.Sid.SubAuthority(uint32(label.Label.Sid.SubAuthorityCount() - 1))
}

func TestWindowsJobObjectTerminatesDescendantProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	quotedPIDFile := strings.ReplaceAll(pidFile, "'", "''")
	script := fmt.Sprintf(
		"$child = Start-Process powershell.exe -ArgumentList '-NoLogo','-NoProfile','-Command','Start-Sleep -Seconds 30' -PassThru; [IO.File]::WriteAllText('%s', [string]$child.Id); Wait-Process -Id $child.Id",
		quotedPIDFile,
	)
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-Command", script)
	process, err := Start(cmd, Limits{MemoryBytes: 768 * 1024 * 1024, MaxProcesses: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Terminate() }()
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()

	var childPID uint64
	deadline := time.NewTimer(15 * time.Second)
	ticker := time.NewTicker(40 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for childPID == 0 {
		raw, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			childPID, err = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
			if err == nil && childPID > 0 {
				break
			}
		}
		select {
		case waitErr := <-waited:
			t.Fatalf("contained root process exited before descendant startup: %v", waitErr)
		case <-deadline.C:
			_ = process.Terminate()
			<-waited
			t.Fatal("descendant process did not start within 15 seconds")
		case <-ticker.C:
		}
	}
	if !windowsProcessRunning(uint32(childPID)) {
		t.Fatal("descendant process exited before containment test")
	}

	if err := process.Terminate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("contained root process did not exit after Job Object termination")
	}
	terminationDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(terminationDeadline) && windowsProcessRunning(uint32(childPID)) {
		time.Sleep(40 * time.Millisecond)
	}
	if windowsProcessRunning(uint32(childPID)) {
		t.Fatalf("descendant process %d survived Job Object termination", childPID)
	}
}

func windowsProcessRunning(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == 259
}

func TestWindowsCapabilitiesAreExplicit(t *testing.T) {
	capabilities := DetectCapabilities()
	if capabilities.Backend != "windows-job-object" || !capabilities.ProcessTree || !capabilities.ResourceLimits {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if capabilities.FilesystemIsolation || capabilities.NetworkIsolation {
		t.Fatalf("P0 must not claim unavailable OS boundaries: %#v", capabilities)
	}
}
