package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
)

const maxCommandOutputBytes = 4 * 1024 * 1024

// RunCommandTool 是唯一的 shell 通道：只负责执行命令，绝不用于读写文本文件。
// 文件读写走编码安全层（read_file/write_file/apply_patch），二者严格分离，
// 避免 shell 重定向破坏编码。命令输出单独用 DecodeCommandOutput 解码（Windows 常为 GBK）。
type RunCommandTool struct{ Policy SandboxPolicy }

func (t RunCommandTool) Name() string { return "run_command" }
func (t RunCommandTool) Description() string {
	return "在工作区内执行 build、test、编译器或程序。优先使用 executable + args 逐参数启动，尤其是 python -c、中文路径或含引号参数；只有管道等 Shell 语法才使用 command。两种模式互斥。文本读取、目录列表、搜索、写入、复制和删除必须使用结构化文件工具。"
}
func (t RunCommandTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "必须且只能提供 command 或 executable 其中一种执行模式",
		"additionalProperties": false,
		"properties": map[string]any{
			"command":    map[string]any{"type": "string", "description": "Shell 命令字符串；仅在需要管道、条件执行等 Shell 语法时使用，不能与 executable 同时提供"},
			"executable": map[string]any{"type": "string", "description": "直接启动的程序名或路径；参数不会经过 Shell 解析，不能与 command 同时提供"},
			"args": map[string]any{
				"type":        "array",
				"description": "直接逐项传给 executable 的参数；仅用于结构化进程模式",
				"items":       map[string]any{"type": "string"},
			},
			"working_directory": map[string]any{"type": "string", "description": "可选工作目录，必须位于工作区或额外允许根内；默认当前工作区"},
		},
	}
}

type runCommandArguments struct {
	Command          string   `json:"command,omitempty"`
	Executable       string   `json:"executable,omitempty"`
	Args             []string `json:"args,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
}

func (t RunCommandTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args runCommandArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	commandMode := strings.TrimSpace(args.Command) != ""
	directMode := strings.TrimSpace(args.Executable) != ""
	if commandMode == directMode {
		return errorResult("必须且只能提供 command 或 executable 其中一种执行模式"), nil
	}
	if commandMode && len(args.Args) > 0 {
		return errorResult("Shell command 模式不能提供 args；请改用 executable + args"), nil
	}
	if err := t.Policy.CheckShell(); err != nil {
		return errorResult(err.Error()), nil
	}
	workDir, err := resolveCommandWorkingDirectory(t.Policy, args.WorkingDirectory)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	displayCommand := runCommandDisplay(args)
	if directMode {
		if err := validateDirectProcessArguments(args.Executable, args.Args); err != nil {
			return errorResult(err.Error()), nil
		}
		if err := t.Policy.ValidateDirectCommandAt(args.Executable, args.Args, workDir); err != nil {
			return errorResult(err.Error()), nil
		}
	} else {
		if err := t.Policy.ValidateCommandAt(args.Command, workDir); err != nil {
			return errorResult(err.Error()), nil
		}
	}

	timeout := time.Duration(t.Policy.MaxCommandSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if commandMode {
		cmd = buildShellCommand(runCtx, args.Command)
	} else {
		cmd = buildDirectCommand(runCtx, args.Executable, args.Args)
	}
	cmd.Dir = workDir
	cmd.Env = safeCommandEnvironment()

	var stdout, stderr cappedCommandOutput
	stdout.limit = maxCommandOutputBytes / 2
	stderr.limit = maxCommandOutputBytes / 2
	startedAt := time.Now()
	reporter := &commandProgressReporter{
		ctx:         ctx,
		command:     displayCommand,
		workDir:     workDir,
		startedAt:   startedAt,
		stdout:      &stdout,
		stderr:      &stderr,
		minInterval: 120 * time.Millisecond,
	}
	stdout.notify = reporter.emit
	stderr.notify = reporter.emit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	reporter.emitNow()
	process, startErr := sandboxexec.Start(cmd, t.Policy.ProcessLimits())
	runErr := startErr
	if startErr == nil {
		runErr = process.Wait()
	}
	completedAt := time.Now()
	stdoutOutput := DecodeCommandOutput(stdout.Bytes())
	stderrOutput := DecodeCommandOutput(stderr.Bytes())
	output := commandOutputForDisplay(stdoutOutput, stderrOutput)
	if dropped := stdout.Dropped() + stderr.Dropped(); dropped > 0 {
		output += fmt.Sprintf("\n... [command output truncated: %d bytes]", dropped)
	}

	exitCode := 0
	status := "ok"
	if runErr != nil {
		status = "error"
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			if strings.TrimSpace(output) == "" && startErr != nil {
				output = startErr.Error()
			}
			if runCtx.Err() == context.DeadlineExceeded {
				output = output + "\n（命令超时被终止）"
			}
		}
	}

	summary := fmt.Sprintf("$ %s\n退出码 %d", displayCommand, exitCode)
	return Result{
		Summary: summary,
		IsError: status == "error",
		Parts: []ResultPart{
			{
				Kind:             PartToolCall,
				Name:             t.Name(),
				Status:           status,
				Input:            displayCommand,
				Output:           output,
				Stdout:           stdoutOutput,
				Stderr:           stderrOutput,
				WorkingDirectory: workDir,
				ExitCode:         intPointer(exitCode),
				StartedAt:        startedAt.Format(time.RFC3339Nano),
				CompletedAt:      completedAt.Format(time.RFC3339Nano),
				DurationMs:       elapsedMilliseconds(startedAt, completedAt),
			},
		},
	}, nil
}

// RunCommandInputForDisplay returns the command label used by approval prompts
// and live tool cards. It is display-only and never reconstructs argv for use
// as an executable command line.
func RunCommandInputForDisplay(rawArgs json.RawMessage) string {
	var args runCommandArguments
	if json.Unmarshal(rawArgs, &args) != nil {
		return ""
	}
	return runCommandDisplay(args)
}

func runCommandDisplay(args runCommandArguments) string {
	if command := strings.TrimSpace(args.Command); command != "" {
		return command
	}
	return formatDirectCommand(args.Executable, args.Args)
}

func formatDirectCommand(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, commandTokenForDisplay(strings.TrimSpace(executable)))
	for _, argument := range args {
		parts = append(parts, commandTokenForDisplay(argument))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func commandTokenForDisplay(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n\"'`") {
		return value
	}
	return strconv.Quote(value)
}

func validateDirectProcessArguments(executable string, args []string) error {
	if strings.ContainsRune(executable, '\x00') {
		return fmt.Errorf("executable 包含无效的 NUL 字符")
	}
	for index, argument := range args {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("args[%d] 包含无效的 NUL 字符", index)
		}
	}
	return nil
}

func resolveCommandWorkingDirectory(policy SandboxPolicy, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = "."
	}
	workDir, err := policy.ResolveCommandWorkingDirectory(requested)
	if err != nil {
		return "", fmt.Errorf("invalid working_directory: %w", err)
	}
	info, err := os.Stat(workDir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("working_directory is not an accessible directory: %s", workDir)
	}
	return workDir, nil
}

func intPointer(value int) *int {
	return &value
}

func elapsedMilliseconds(startedAt, completedAt time.Time) int64 {
	duration := completedAt.Sub(startedAt).Milliseconds()
	if duration < 1 {
		return 1
	}
	return duration
}

type cappedCommandOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	limit   int
	written int
	notify  func()
}

func (w *cappedCommandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.written += len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.buffer.Write(p[:remaining])
	}
	notify := w.notify
	w.mu.Unlock()
	if notify != nil {
		notify()
	}
	return len(p), nil
}

func (w *cappedCommandOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (w *cappedCommandOutput) Dropped() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.written <= w.buffer.Len() {
		return 0
	}
	return w.written - w.buffer.Len()
}

type commandProgressReporter struct {
	ctx          context.Context
	toolName     string
	command      string
	workDir      string
	startedAt    time.Time
	stdout       *cappedCommandOutput
	stderr       *cappedCommandOutput
	minInterval  time.Duration
	outputFilter func(string) string

	mu       sync.Mutex
	lastEmit time.Time
}

func (r *commandProgressReporter) emit() {
	r.publish(false)
}

func (r *commandProgressReporter) emitNow() {
	r.publish(true)
}

func (r *commandProgressReporter) publish(force bool) {
	now := time.Now()
	r.mu.Lock()
	if !force && !r.lastEmit.IsZero() && now.Sub(r.lastEmit) < r.minInterval {
		r.mu.Unlock()
		return
	}
	r.lastEmit = now
	r.mu.Unlock()

	stdout := DecodeCommandOutput(r.stdout.Bytes())
	stderr := DecodeCommandOutput(r.stderr.Bytes())
	if r.outputFilter != nil {
		stdout = r.outputFilter(stdout)
		stderr = r.outputFilter(stderr)
	}
	toolName := strings.TrimSpace(r.toolName)
	if toolName == "" {
		toolName = "run_command"
	}
	EmitProgress(r.ctx, ResultPart{
		Kind:             PartToolCall,
		Name:             toolName,
		Status:           "running",
		Input:            r.command,
		Output:           commandOutputForDisplay(stdout, stderr),
		Stdout:           stdout,
		Stderr:           stderr,
		WorkingDirectory: r.workDir,
		StartedAt:        r.startedAt.Format(time.RFC3339Nano),
	})
}

func commandOutputForDisplay(stdout, stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return stdout
	}
	if strings.TrimSpace(stdout) == "" {
		return stderr
	}
	return strings.TrimRight(stdout, "\r\n") + "\n[stderr]\n" + stderr
}

func safeCommandEnvironment() []string {
	filtered := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if !ok || commandEnvironmentIsSensitive(name) || commandEnvironmentIsOverridden(name) {
			continue
		}
		filtered = append(filtered, item)
	}
	filtered = append(filtered,
		"PYTHONUTF8=1",
		"PYTHONIOENCODING=utf-8",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"NO_COLOR=1",
	)
	return filtered
}

func commandEnvironmentIsOverridden(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "PYTHONUTF8", "PYTHONIOENCODING", "GIT_PAGER", "PAGER", "NO_COLOR":
		return true
	default:
		return false
	}
}

// SafeCommandEnvironment returns the environment exposed to agent-controlled
// processes. Interactive terminals started directly by the user may choose a
// different policy.
func SafeCommandEnvironment() []string {
	return safeCommandEnvironment()
}

func commandEnvironmentIsSensitive(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	for _, marker := range []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "AUTHORIZATION"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// buildShellCommand 按平台选择 shell 解释器。
func buildShellCommand(ctx context.Context, command string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// UTF-8 code page covers most modern CLI programs. DecodeCommandOutput
		// still handles UTF-16 and legacy GB18030 emitters.
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "chcp 65001>nul & "+command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	hideConsoleWindow(cmd) // Windows 上隐藏弹出的 cmd 黑框
	return cmd
}

func buildDirectCommand(ctx context.Context, executable string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, strings.TrimSpace(executable), args...)
	hideConsoleWindow(cmd)
	return cmd
}
