//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/tools"
	"golang.org/x/sys/windows"
)

func TestChatTaskStopTerminatesRealCommandTreeAndPersistsOneTerminalState(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(workspace, "release-stop-child.pid")
	toolArguments, err := json.Marshal(map[string]any{
		"executable": executable,
		"args": []string{
			"-test.run=^TestChatTaskStopReleaseHelperProcess$",
			"--",
			"__spawn_child__",
			pidFile,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0,
					"id":    "release-stop-call",
					"type":  "function",
					"function": map[string]any{
						"name":      "run_command",
						"arguments": string(toolArguments),
					},
				}}},
				"finish_reason": "tool_calls",
			}},
		}
		encoded, marshalErr := json.Marshal(chunk)
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer server.Close()

	service := agent.NewService(agent.ServiceConfig{
		SkillsDir:              filepath.Join(base, "skills"),
		SessionsDir:            filepath.Join(base, "sessions"),
		ProjectsPath:           filepath.Join(base, "projects.json"),
		SettingsPath:           filepath.Join(base, "runtime-settings.json"),
		TemporaryWorkspaceRoot: filepath.Join(base, "temporary"),
	})
	defer service.Close()
	settings := service.WorkbenchState().RuntimeSettings
	settings.SandboxMode = "workspace-write"
	settings.FilesystemAccess = "workspace-write"
	settings.NetworkAccess = true
	settings.ShellAccess = true
	settings.ApprovalPolicy = "never"
	settings.ToolTimeoutSeconds = 30
	settings.TaskIdleTimeoutSeconds = 60
	settings.MaxCommandSeconds = 30
	settings.MaxCommandProcesses = 8
	settings.Team.Enabled = false
	settings.Model = agent.ModelSettings{
		SelectedProviderID: "release-stop-local",
		SelectedModelID:    "release-stop-model",
		Providers: []agent.ModelProviderSetting{{
			ID: "release-stop-local", Name: "Release stop local", Protocol: "local",
			APIType: "chat-completions", BaseURL: server.URL, Enabled: true,
			DefaultModelID: "release-stop-model",
			Models: []agent.ProviderModel{{
				ID: "release-stop-model", DisplayName: "Release stop model",
				Provider: "release-stop-local", ContextWindowTokens: 128_000,
			}},
		}},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject("release-stop", workspace)
	if err != nil {
		t.Fatal(err)
	}
	projectID, sessionID := state.ActiveProjectID, state.ActiveSessionID

	app := &App{service: service}
	taskID, err := app.StartChatMessageForProjectSession(projectID, sessionID, "运行长命令，等待我停止")
	if err != nil {
		t.Fatal(err)
	}

	runtimeService := activeTaskService(t, app, taskID)
	childPID := waitForReleaseStopChild(t, pidFile)
	if !windowsProcessIsRunning(childPID) {
		t.Fatalf("child process %d exited before stop", childPID)
	}
	if !taskHasRunningCommand(app.GetActiveChatTask()) {
		t.Fatal("running command was not visible in the live task timeline")
	}

	stopStarted := time.Now()
	if !app.StopChatMessage(taskID) {
		t.Fatal("stop request was not accepted")
	}
	if elapsed := time.Since(stopStarted); elapsed > 250*time.Millisecond {
		t.Fatalf("stop request blocked for %s", elapsed)
	}
	waitForChatTaskExit(t, app, taskID, 6*time.Second)
	waitForWindowsProcessExit(t, childPID, 5*time.Second)

	runtimeState, ok := runtimeService.TaskRuntimeSnapshot()
	if !ok || runtimeState.Status != "cancelled" {
		t.Fatalf("task runtime = %#v, ok=%v", runtimeState, ok)
	}
	commandParts := 0
	for _, part := range runtimeState.Parts {
		if part.Kind == tools.PartToolCall && part.Name == "run_command" {
			commandParts++
			if part.Status != "cancelled" || part.CompletedAt == "" || part.DurationMs <= 0 ||
				!strings.Contains(part.Stderr, "工具已因任务停止而中断") {
				t.Fatalf("terminal command part = %#v", part)
			}
		}
	}
	if commandParts != 1 {
		t.Fatalf("run_command parts = %d, want 1: %#v", commandParts, runtimeState.Parts)
	}

	history, err := service.GetSessionMessagesForProjectSession(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	cancelledTurns := 0
	for _, message := range history {
		if message.Status == "cancelled" {
			cancelledTurns++
		}
	}
	if cancelledTurns != 1 {
		t.Fatalf("cancelled terminal turns = %d, want 1: %#v", cancelledTurns, history)
	}
	if requests.Load() != 1 {
		t.Fatalf("provider requests after cancellation = %d, want 1", requests.Load())
	}
}

func TestChatTaskStopReleaseHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	arguments := os.Args[separator+1:]
	switch arguments[0] {
	case "__sleep_child__":
		time.Sleep(30 * time.Second)
	case "__spawn_child__":
		if len(arguments) != 2 {
			os.Exit(2)
		}
		executable, err := os.Executable()
		if err != nil {
			os.Exit(3)
		}
		child := exec.Command(executable,
			"-test.run=^TestChatTaskStopReleaseHelperProcess$", "--", "__sleep_child__")
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(arguments[1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(5)
		}
		_ = child.Wait()
	}
}

func activeTaskService(t *testing.T, app *App, taskID string) *agent.Service {
	t.Helper()
	app.chat.mu.Lock()
	defer app.chat.mu.Unlock()
	task := app.chat.tasks[taskID]
	if task == nil || task.service == nil {
		t.Fatalf("active task %s has no runtime service", taskID)
	}
	return task.service
}

func waitForReleaseStopChild(t *testing.T, pidFile string) uint32 {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			pid, parseErr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
			if parseErr == nil && pid > 0 {
				return uint32(pid)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for command descendant process")
	return 0
}

func taskHasRunningCommand(state *ChatTaskState) bool {
	if state == nil {
		return false
	}
	for _, part := range state.Parts {
		if part.Kind == tools.PartToolCall && part.Name == "run_command" && part.Status == "running" {
			return true
		}
	}
	return false
}

func waitForChatTaskExit(t *testing.T, app *App, taskID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active := false
		for _, state := range app.GetActiveChatTasks() {
			if state.TaskID == taskID {
				active = true
				break
			}
		}
		if !active {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("chat task %s did not stop within %s", taskID, timeout)
}

func windowsProcessIsRunning(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}

func waitForWindowsProcessExit(t *testing.T, pid uint32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !windowsProcessIsRunning(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived task cancellation", pid)
}
