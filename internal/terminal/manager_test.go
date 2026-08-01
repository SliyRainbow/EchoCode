package terminal

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const terminalTestTimeout = 15 * time.Second

func TestManagerRunsPersistentWorkspaceShell(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	root := t.TempDir()
	state, err := manager.Start(root)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && (!state.Sandboxed || state.SandboxBackend != "windows-job-object") {
		t.Fatalf("terminal containment state = %#v", state)
	}
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "Get-Location"
	}
	if err := manager.WriteLine(state.ID, command); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(terminalTestTimeout)
	for time.Now().Before(deadline) {
		state, err = manager.State(state.ID)
		if err != nil {
			t.Fatal(err)
		}
		normalizedOutput := strings.ToLower(filepath.Clean(strings.TrimSpace(state.Output)))
		normalizedRoot := strings.ToLower(filepath.Clean(state.Workdir))
		if strings.Contains(normalizedOutput, normalizedRoot) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(strings.ToLower(filepath.Clean(state.Output)), strings.ToLower(filepath.Clean(state.Workdir))) {
		t.Fatalf("terminal output = %q, want workspace %q", state.Output, state.Workdir)
	}
	if err := manager.Stop(state.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerPushesOutputAndLifecycleUpdates(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	updates := make(chan SessionState, 32)
	manager.SetNotify(func(state SessionState) {
		select {
		case updates <- state:
		default:
		}
	})
	state, err := manager.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteLine(state.ID, "echo ECHOCODE_TERMINAL_EVENT"); err != nil {
		t.Fatal(err)
	}

	outputSeen := false
	deadline := time.After(terminalTestTimeout)
	for !outputSeen {
		select {
		case update := <-updates:
			outputSeen = update.ID == state.ID && strings.Contains(update.Output, "ECHOCODE_TERMINAL_EVENT")
		case <-deadline:
			t.Fatal("timed out waiting for terminal output event")
		}
	}
	if err := manager.Stop(state.ID); err != nil {
		t.Fatal(err)
	}
	deadline = time.After(terminalTestTimeout)
	for {
		select {
		case update := <-updates:
			if update.ID == state.ID && !update.Running {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for terminal exit event")
		}
	}
}

func TestManagerPreservesUnicodeOutput(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	state, err := manager.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteLine(state.ID, "echo EchoCode编码测试"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(terminalTestTimeout)
	for time.Now().Before(deadline) {
		state, err = manager.State(state.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(state.Output, "EchoCode编码测试") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("terminal Unicode output = %q", state.Output)
}

func TestManagerStopWaitsForActiveChildProcess(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	state, err := manager.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = `powershell -NoProfile -Command "Start-Sleep -Seconds 30"`
	}
	if err := manager.WriteLine(state.ID, command); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)

	started := time.Now()
	if err := manager.Stop(state.ID); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("stopping terminal took %v", elapsed)
	}
	state, err = manager.State(state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Running {
		t.Fatal("Stop returned while terminal was still running")
	}
}

func TestManagerRejectsMoreThanMaximumRunningSessions(t *testing.T) {
	manager := NewManager()
	for index := 0; index < maxSessions; index++ {
		id := fmt.Sprintf("session-%d", index)
		manager.sessions[id] = &session{id: id, running: true, startedAt: fmt.Sprintf("%02d", index)}
	}
	if err := manager.reserveStart(); err == nil {
		t.Fatal("expected terminal session limit error")
	}
	if manager.starting != 0 {
		t.Fatalf("starting = %d, want 0", manager.starting)
	}
}
