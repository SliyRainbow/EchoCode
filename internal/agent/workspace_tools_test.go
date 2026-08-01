package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
	"github.com/SliyRainbow/EchoCode/internal/terminal"
	"github.com/SliyRainbow/EchoCode/internal/tools"
	"github.com/SliyRainbow/EchoCode/internal/workspacegit"
)

type gitControllerStub struct{}

func (gitControllerStub) Status(context.Context, string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true, Branch: "main"}, nil
}
func (gitControllerStub) Diff(context.Context, string, string, bool) (workspacegit.Diff, error) {
	return workspacegit.Diff{Patch: "diff"}, nil
}
func (gitControllerStub) Stage(context.Context, string, []string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true}, nil
}
func (gitControllerStub) Unstage(context.Context, string, []string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true}, nil
}
func (gitControllerStub) Commit(context.Context, string, string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true}, nil
}
func (gitControllerStub) CreateBranch(context.Context, string, string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true}, nil
}
func (gitControllerStub) SwitchBranch(context.Context, string, string) (workspacegit.Status, error) {
	return workspacegit.Status{Available: true}, nil
}

type terminalControllerStub struct {
	writes int
	state  terminal.SessionState
}

func (t *terminalControllerStub) StartRestricted(string, sandboxexec.Limits) (terminal.SessionState, error) {
	if t.state.ID != "" {
		return t.state, nil
	}
	return terminal.SessionState{ID: "session"}, nil
}
func (t *terminalControllerStub) State(string) (terminal.SessionState, error) {
	if t.state.ID != "" {
		return t.state, nil
	}
	return terminal.SessionState{ID: "session"}, nil
}
func (t *terminalControllerStub) WriteLine(string, string) error { t.writes++; return nil }
func (t *terminalControllerStub) Stop(string) error              { return nil }

func TestGitToolReadOnlyModeRejectsMutation(t *testing.T) {
	tool := GitTool{Policy: tools.SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "read-only"}, Controller: gitControllerStub{}, ReadOnlyOnly: true}
	args, _ := json.Marshal(map[string]any{"action": "stage", "paths": []string{"a.txt"}})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTerminalToolAppliesCommandPolicy(t *testing.T) {
	controller := &terminalControllerStub{}
	tool := TerminalTool{Policy: tools.SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true}, Controller: controller}
	args, _ := json.Marshal(map[string]any{"action": "write", "session_id": "session", "command": "del important.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.IsError || controller.writes != 0 {
		t.Fatalf("result=%#v writes=%d err=%v", result, controller.writes, err)
	}
}

func TestTerminalToolReturnsStructuredSessionMetadata(t *testing.T) {
	root := t.TempDir()
	exitCode := 7
	controller := &terminalControllerStub{state: terminal.SessionState{
		ID: "session", Workdir: root, Running: false, StartedAt: "2026-07-23T12:00:00Z",
		ExitCode: exitCode, Output: "stdout line", Error: "stderr line",
	}}
	tool := TerminalTool{Policy: tools.SandboxPolicy{WorkspaceRoot: root}, Controller: controller}
	args, _ := json.Marshal(map[string]any{"action": "state", "session_id": "session"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError || len(result.Parts) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	part := result.Parts[0]
	if part.Input != "读取终端状态 session" || part.WorkingDirectory != root || part.Stdout != "stdout line" || part.Stderr != "stderr line" ||
		part.StartedAt != "2026-07-23T12:00:00Z" || part.ExitCode == nil || *part.ExitCode != exitCode {
		t.Fatalf("terminal metadata = %#v", part)
	}
}

func TestAgentToolLoopWritesStagesCommitsAndReturnsFinalReply(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	workspace := t.TempDir()
	runAgentTestGit(t, workspace, "init")
	runAgentTestGit(t, workspace, "config", "user.email", "echocode@example.invalid")
	runAgentTestGit(t, workspace, "config", "user.name", "EchoCode Agent Test")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runAgentTestGit(t, workspace, "add", "README.md")
	runAgentTestGit(t, workspace, "commit", "-m", "initial")

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir(), Git: workspacegit.Service{}})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.SandboxMode = "workspace-write"
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	svc.runtimeSettings.NetworkAccess = false
	svc.runtimeSettings.ShellAccess = false

	completion := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completion++
		last := request.Messages[len(request.Messages)-1]
		switch completion {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "write-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "write_file", Arguments: json.RawMessage(`{"path":"agent.txt","content":"created by agent\n"}`),
				},
			}}}, nil
		case 2:
			if last.Role != "tool" || last.Name != "write_file" || !strings.Contains(last.Content, "agent.txt") {
				t.Fatalf("write feedback = %#v", last)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "git-stage", Type: "function", Function: protocol.ToolCallFunction{
					Name: "git", Arguments: json.RawMessage(`{"action":"stage","paths":["agent.txt"]}`),
				},
			}}}, nil
		case 3:
			if last.Role != "tool" || last.Name != "git" || !strings.Contains(last.Content, "Git stage completed") {
				t.Fatalf("stage feedback = %#v", last)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "git-commit", Type: "function", Function: protocol.ToolCallFunction{
					Name: "git", Arguments: json.RawMessage(`{"action":"commit","message":"agent integration"}`),
				},
			}}}, nil
		default:
			if last.Role != "tool" || last.Name != "git" || !strings.Contains(last.Content, "Git commit completed") {
				t.Fatalf("commit feedback = %#v", last)
			}
			return protocol.CompletionResult{Content: "文件已创建并提交。"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), svc.buildToolRegistry(), protocol.ChatRequest{
		Model: "integration-model", Messages: []protocol.Message{{Role: "user", Content: "创建并提交文件"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "文件已创建并提交。" || completion != 4 {
		t.Fatalf("outcome=%#v completions=%d", outcome, completion)
	}
	status, err := (workspacegit.Service{}).Status(context.Background(), workspace)
	if err != nil || !status.Clean {
		t.Fatalf("git status=%#v err=%v", status, err)
	}
	logOutput := runAgentTestGit(t, workspace, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(logOutput) != "agent integration" {
		t.Fatalf("last commit = %q", logOutput)
	}
}

func runAgentTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
