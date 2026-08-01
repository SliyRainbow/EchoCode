package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
	"github.com/SliyRainbow/EchoCode/internal/terminal"
	"github.com/SliyRainbow/EchoCode/internal/tools"
	"github.com/SliyRainbow/EchoCode/internal/workspacegit"
)

type GitController interface {
	Status(context.Context, string) (workspacegit.Status, error)
	Diff(context.Context, string, string, bool) (workspacegit.Diff, error)
	Stage(context.Context, string, []string) (workspacegit.Status, error)
	Unstage(context.Context, string, []string) (workspacegit.Status, error)
	Commit(context.Context, string, string) (workspacegit.Status, error)
	CreateBranch(context.Context, string, string) (workspacegit.Status, error)
	SwitchBranch(context.Context, string, string) (workspacegit.Status, error)
}

type TerminalController interface {
	StartRestricted(string, sandboxexec.Limits) (terminal.SessionState, error)
	State(string) (terminal.SessionState, error)
	WriteLine(string, string) error
	Stop(string) error
}

type GitTool struct {
	Policy       tools.SandboxPolicy
	Controller   GitController
	ReadOnlyOnly bool
}

func (t GitTool) Name() string { return "git" }
func (t GitTool) Description() string {
	return "使用受工作区约束的 Git 接口查看状态、diff、暂存、取消暂存、提交以及创建或切换分支。"
}
func (t GitTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"status", "diff", "stage", "unstage", "commit", "create_branch", "switch_branch"}},
			"path":    map[string]any{"type": "string"},
			"staged":  map[string]any{"type": "boolean"},
			"paths":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"message": map[string]any{"type": "string"},
			"name":    map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}
func (t GitTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.Controller == nil {
		return toolError(t.Name(), "Git controller is unavailable"), nil
	}
	var args struct {
		Action  string   `json:"action"`
		Path    string   `json:"path"`
		Staged  bool     `json:"staged"`
		Paths   []string `json:"paths"`
		Message string   `json:"message"`
		Name    string   `json:"name"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return toolError(t.Name(), "invalid git arguments: "+err.Error()), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if t.ReadOnlyOnly && gitActionMutates(action) {
		return toolError(t.Name(), "当前 Git 工具仅允许查看状态和差异"), nil
	}
	if gitActionMutates(action) && strings.EqualFold(t.Policy.FilesystemAccess, "read-only") {
		return toolError(t.Name(), "Git write operations are disabled in read-only mode"), nil
	}
	var value any
	var err error
	switch action {
	case "status":
		value, err = t.Controller.Status(ctx, t.Policy.WorkspaceRoot)
	case "diff":
		value, err = t.Controller.Diff(ctx, t.Policy.WorkspaceRoot, args.Path, args.Staged)
	case "stage":
		value, err = t.Controller.Stage(ctx, t.Policy.WorkspaceRoot, args.Paths)
	case "unstage":
		value, err = t.Controller.Unstage(ctx, t.Policy.WorkspaceRoot, args.Paths)
	case "commit":
		value, err = t.Controller.Commit(ctx, t.Policy.WorkspaceRoot, args.Message)
	case "create_branch":
		value, err = t.Controller.CreateBranch(ctx, t.Policy.WorkspaceRoot, args.Name)
	case "switch_branch":
		value, err = t.Controller.SwitchBranch(ctx, t.Policy.WorkspaceRoot, args.Name)
	default:
		return toolError(t.Name(), "unsupported Git action: "+action), nil
	}
	if err != nil {
		return toolError(t.Name(), err.Error()), nil
	}
	encoded, _ := json.Marshal(value)
	return tools.Result{
		Summary: fmt.Sprintf("Git %s completed", action),
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: t.Name(), Status: "ok", Input: string(rawArgs), Output: string(encoded)}},
	}, nil
}

func gitActionMutates(action string) bool {
	switch action {
	case "stage", "unstage", "commit", "create_branch", "switch_branch":
		return true
	default:
		return false
	}
}

type TerminalTool struct {
	Policy     tools.SandboxPolicy
	Controller TerminalController
}

func (t TerminalTool) Name() string { return "terminal" }
func (t TerminalTool) Description() string {
	return "管理持久终端会话：启动、读取状态、发送一条命令或停止会话。发送命令前会执行工作区命令安全检查。"
}
func (t TerminalTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":     map[string]any{"type": "string", "enum": []string{"start", "state", "write", "stop"}},
			"session_id": map[string]any{"type": "string"},
			"command":    map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}
func (t TerminalTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.Controller == nil {
		return toolError(t.Name(), "terminal controller is unavailable"), nil
	}
	var args struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return toolError(t.Name(), "invalid terminal arguments: "+err.Error()), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	var value any
	var err error
	switch action {
	case "start":
		if checkErr := t.Policy.CheckShell(); checkErr != nil {
			return toolError(t.Name(), checkErr.Error()), nil
		}
		value, err = t.Controller.StartRestricted(t.Policy.WorkspaceRoot, t.Policy.ProcessLimits())
	case "state":
		value, err = t.Controller.State(args.SessionID)
	case "write":
		if checkErr := t.Policy.CheckShell(); checkErr != nil {
			return toolError(t.Name(), checkErr.Error()), nil
		}
		if checkErr := t.Policy.ValidateCommand(args.Command); checkErr != nil {
			return toolError(t.Name(), checkErr.Error()), nil
		}
		err = t.Controller.WriteLine(args.SessionID, args.Command)
		value = map[string]string{"sessionId": args.SessionID, "status": "sent"}
	case "stop":
		err = t.Controller.Stop(args.SessionID)
		value = map[string]string{"sessionId": args.SessionID, "status": "stopped"}
	default:
		return toolError(t.Name(), "unsupported terminal action: "+action), nil
	}
	if err != nil {
		return toolError(t.Name(), err.Error()), nil
	}
	encoded, _ := json.Marshal(value)
	part := tools.ResultPart{
		Kind: tools.PartToolCall, Name: t.Name(), Status: "ok",
		Input: terminalActionDisplay(action, args.SessionID, args.Command), Output: string(encoded),
	}
	if state, ok := value.(terminal.SessionState); ok {
		part.WorkingDirectory = state.Workdir
		part.Stdout = state.Output
		part.StartedAt = state.StartedAt
		if state.Error != "" {
			part.Stderr = state.Error
		}
		if !state.Running {
			exitCode := state.ExitCode
			part.ExitCode = &exitCode
		}
	}
	return tools.Result{Summary: fmt.Sprintf("terminal %s completed", action), Parts: []tools.ResultPart{part}}, nil
}

func terminalActionDisplay(action, sessionID, command string) string {
	if command = strings.TrimSpace(command); command != "" {
		return command
	}
	sessionID = strings.TrimSpace(sessionID)
	switch action {
	case "start":
		return "启动持久终端"
	case "state":
		return strings.TrimSpace("读取终端状态 " + sessionID)
	case "stop":
		return strings.TrimSpace("停止持久终端 " + sessionID)
	default:
		return strings.TrimSpace(action + " " + sessionID)
	}
}

func toolError(name, message string) tools.Result {
	return tools.Result{Summary: message, IsError: true, Parts: []tools.ResultPart{{Kind: tools.PartToolCall, Name: name, Status: "error", Output: message}}}
}
