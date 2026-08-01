package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCommandShellDisabled(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), ShellAccess: false}
	tool := RunCommandTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{"command": "echo hi"})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError {
		t.Fatal("ShellAccess=false 时应拒绝执行")
	}
}

func TestRunCommandRoutesRemoteOperationsToStructuredSSH(t *testing.T) {
	policy := SandboxPolicy{
		WorkspaceRoot:    t.TempDir(),
		FilesystemAccess: "workspace-write",
		NetworkAccess:    true,
		ShellAccess:      true,
	}
	for _, command := range []string{"ssh root@example.com", "scp index.html root@example.com:/var/www", "sftp root@example.com", "ssh-add -L"} {
		err := policy.ValidateCommand(command)
		if !errors.Is(err, ErrShellRemoteOperation) {
			t.Fatalf("command %q error = %v, want ErrShellRemoteOperation", command, err)
		}
	}
}

func TestSandboxPolicyBuildsProcessLimits(t *testing.T) {
	policy := SandboxPolicy{MaxCommandMemoryMB: 2048, MaxCommandCPUPercent: 65, MaxCommandProcesses: 24}
	limits := policy.ProcessLimits()
	if limits.MemoryBytes != 2048*1024*1024 || limits.CPUPercent != 65 || limits.MaxProcesses != 24 || !limits.RestrictPrivileges {
		t.Fatalf("limits = %#v", limits)
	}
	policy.SandboxMode = "danger-full-access"
	if policy.ProcessLimits().RestrictPrivileges {
		t.Fatal("danger-full-access unexpectedly uses a restricted token")
	}
}

func TestRunCommandEcho(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true, MaxCommandSeconds: 30}
	tool := RunCommandTool{Policy: policy}

	command := "echo echocode-ok"
	if runtime.GOOS == "windows" {
		command = "echo echocode-ok"
	}
	args, _ := json.Marshal(map[string]string{"command": command})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if res.IsError {
		t.Fatalf("不应报错: %s", res.Summary)
	}
	if len(res.Parts) != 1 || res.Parts[0].Kind != PartToolCall {
		t.Fatalf("应产出 tool_call part: %+v", res.Parts)
	}
	if !strings.Contains(res.Parts[0].Output, "echocode-ok") {
		t.Fatalf("输出应含 echocode-ok: %q", res.Parts[0].Output)
	}
	part := res.Parts[0]
	if part.WorkingDirectory == "" || part.WorkingDirectory != policy.WorkspaceRoot {
		t.Fatalf("working directory = %q, want %q", part.WorkingDirectory, policy.WorkspaceRoot)
	}
	if part.ExitCode == nil || *part.ExitCode != 0 {
		t.Fatalf("exit code = %#v, want 0", part.ExitCode)
	}
	if part.StartedAt == "" || part.CompletedAt == "" || part.DurationMs < 1 {
		t.Fatalf("execution metadata is incomplete: %#v", part)
	}
}

func TestRunCommandRequiresExactlyOneExecutionMode(t *testing.T) {
	tool := RunCommandTool{Policy: SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", ShellAccess: true}}
	for name, input := range map[string]map[string]any{
		"neither":    {},
		"both":       {"command": "echo shell", "executable": "echo", "args": []string{"direct"}},
		"shell-args": {"command": "echo shell", "args": []string{"unexpected"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(input)
			result, err := tool.Execute(context.Background(), raw)
			if err != nil || !result.IsError {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
		})
	}
}

func TestRunCommandStructuredModePreservesArgumentsAndWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "中文 子目录")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"中文 空格",
		"line one\nline two",
		`single' and "double"`,
		`folder\child\file.txt`,
		"",
	}
	processArgs := append([]string{"-test.run=^TestRunCommandStructuredHelperProcess$", "--"}, wantArgs...)
	raw, _ := json.Marshal(map[string]any{
		"executable":        executable,
		"args":              processArgs,
		"working_directory": "中文 子目录",
	})
	tool := RunCommandTool{Policy: SandboxPolicy{
		WorkspaceRoot: root, FilesystemAccess: "workspace-write", ShellAccess: true, MaxCommandSeconds: 30,
	}}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	var payload struct {
		Args []string `json:"args"`
		CWD  string   `json:"cwd"`
	}
	output := result.Parts[0].Stdout
	marker := "ECHOCODE_STRUCTURED_ARGS="
	start := strings.Index(output, marker)
	if start < 0 {
		t.Fatalf("helper output = %q", output)
	}
	encoded := output[start+len(marker):]
	if end := strings.IndexByte(encoded, '\n'); end >= 0 {
		encoded = encoded[:end]
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("decode helper payload %q: %v", encoded, err)
	}
	if len(payload.Args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", payload.Args, wantArgs)
	}
	for index := range wantArgs {
		if payload.Args[index] != wantArgs[index] {
			t.Fatalf("args[%d] = %q, want %q", index, payload.Args[index], wantArgs[index])
		}
	}
	if filepath.Clean(payload.CWD) != filepath.Clean(workDir) || result.Parts[0].WorkingDirectory != workDir {
		t.Fatalf("cwd = %q part=%q want=%q", payload.CWD, result.Parts[0].WorkingDirectory, workDir)
	}
	if !strings.Contains(result.Parts[0].Input, "中文 空格") || !strings.Contains(result.Parts[0].Input, `\"double\"`) {
		t.Fatalf("display input = %q", result.Parts[0].Input)
	}
}

func TestRunCommandStructuredPythonCodeIsNotRequoted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows python -c quoting regression")
	}
	python, err := exec.LookPath("python.exe")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is not installed")
	}
	payload := "中文 空格 \"double\" 'single' folder\\child\nsecond line"
	code := "import base64,sys\nsys.stdout.write(base64.b64encode(sys.argv[1].encode('utf-8')).decode('ascii'))"
	raw, _ := json.Marshal(map[string]any{
		"executable": python,
		"args":       []string{"-c", code, payload},
	})
	tool := RunCommandTool{Policy: SandboxPolicy{
		WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", ShellAccess: true, MaxCommandSeconds: 30,
	}}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Parts[0].Stdout))
	if decodeErr != nil || string(decoded) != payload {
		t.Fatalf("python argv changed: encoded=%q decoded=%q want=%q err=%v", result.Parts[0].Stdout, decoded, payload, decodeErr)
	}
}

func TestRunCommandStructuredModeRejectsOutsideWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	raw, _ := json.Marshal(map[string]any{
		"executable":        "go",
		"args":              []string{"version"},
		"working_directory": outside,
	})
	tool := RunCommandTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", ShellAccess: true}}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "working_directory") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestRunCommandTaskScopeRequiresTargetWorkingDirectory(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "echocode-agent-web-test")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{
		WorkspaceRoot: workspace, FilesystemAccess: "workspace-write", ShellAccess: true,
		TaskScopeEnabled: true, TaskScopeRoots: []string{target},
	}
	tool := RunCommandTool{Policy: policy}

	workspaceArgs, _ := json.Marshal(map[string]any{
		"executable": "go", "args": []string{"version"}, "working_directory": ".",
	})
	workspaceResult, err := tool.Execute(context.Background(), workspaceArgs)
	if err != nil || !workspaceResult.IsError || !strings.Contains(workspaceResult.Summary, "本轮任务范围") {
		t.Fatalf("workspace command escaped task scope: result=%#v err=%v", workspaceResult, err)
	}

	escapeArgs, _ := json.Marshal(map[string]any{
		"executable": "go", "args": []string{"test", "../existing-project"}, "working_directory": target,
	})
	escapeResult, err := tool.Execute(context.Background(), escapeArgs)
	if err != nil || !escapeResult.IsError || !strings.Contains(escapeResult.Summary, "本轮任务范围") {
		t.Fatalf("parent traversal escaped task scope: result=%#v err=%v", escapeResult, err)
	}

	if resolved, resolveErr := policy.ResolveCommandWorkingDirectory(target); resolveErr != nil || resolved != target {
		t.Fatalf("target working directory resolved=%q err=%v", resolved, resolveErr)
	}
}

func TestRunCommandStructuredModeStillUsesCommandBroker(t *testing.T) {
	tool := RunCommandTool{Policy: SandboxPolicy{
		WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true,
	}}
	for name, input := range map[string]map[string]any{
		"ssh":       {"executable": "ssh", "args": []string{"root@example.com"}},
		"file-read": {"executable": "powershell.exe", "args": []string{"-Command", "Get-Content README.md"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(input)
			result, err := tool.Execute(context.Background(), raw)
			if err != nil || !result.IsError {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
		})
	}
}

func TestRunCommandStructuredModeHonorsCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"executable": executable,
		"args":       []string{"-test.run=^TestRunCommandStructuredHelperProcess$", "--", "__sleep__"},
	})
	tool := RunCommandTool{Policy: SandboxPolicy{
		WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", ShellAccess: true, MaxCommandSeconds: 30,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	result, err := tool.Execute(ctx, raw)
	if err != nil || !result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if elapsed := time.Since(startedAt); elapsed > 4*time.Second {
		t.Fatalf("structured process cancellation took %s", elapsed)
	}
}

func TestRunCommandStructuredHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return
	}
	arguments := append([]string(nil), os.Args[separator+1:]...)
	if len(arguments) == 1 && arguments[0] == "__sleep__" {
		time.Sleep(30 * time.Second)
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Args []string `json:"args"`
		CWD  string   `json:"cwd"`
	}{Args: arguments, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("ECHOCODE_STRUCTURED_ARGS=%s\n", payload)
}

func TestRunCommandStreamsStructuredOutputWhileRunning(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true, MaxCommandSeconds: 30}
	tool := RunCommandTool{Policy: policy}
	command := `printf first; sleep 0.25; printf second >&2`
	if runtime.GOOS == "windows" {
		script := append(append([]byte(nil), utf8BOM...), []byte("Write-Output 'first'\nStart-Sleep -Milliseconds 250\n[Console]::Error.WriteLine('second')\n")...)
		if err := os.WriteFile(filepath.Join(root, "stream-output.ps1"), script, 0o600); err != nil {
			t.Fatal(err)
		}
		command = `powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File stream-output.ps1`
	}
	args, _ := json.Marshal(map[string]string{"command": command})
	var mu sync.Mutex
	progress := make([]ResultPart, 0, 4)
	ctx := WithProgressSink(context.Background(), func(part ResultPart) {
		mu.Lock()
		progress = append(progress, part)
		mu.Unlock()
	})
	result, err := tool.Execute(ctx, args)
	if err != nil || result.IsError {
		t.Fatalf("run result=%#v err=%v", result, err)
	}
	mu.Lock()
	updates := append([]ResultPart(nil), progress...)
	mu.Unlock()
	if len(updates) < 2 {
		t.Fatalf("progress updates = %#v, want start plus output", updates)
	}
	if updates[0].Status != "running" || updates[0].Input != command || updates[0].WorkingDirectory != policy.WorkspaceRoot || updates[0].StartedAt == "" {
		t.Fatalf("initial progress metadata = %#v", updates[0])
	}
	seenFirst := false
	for _, update := range updates {
		seenFirst = seenFirst || strings.Contains(update.Stdout, "first")
	}
	if !seenFirst {
		t.Fatalf("stdout was not streamed: %#v", updates)
	}
	part := result.Parts[0]
	if !strings.Contains(part.Stdout, "first") || !strings.Contains(part.Stderr, "second") {
		t.Fatalf("final stdout/stderr = %q / %q", part.Stdout, part.Stderr)
	}
}

func TestRunCommandRejectsDestructiveOperationByDefault(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true}
	tool := RunCommandTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{"command": "del definitely-not-a-real-file.txt"})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError || (!strings.Contains(res.Summary, "destructive") && !strings.Contains(res.Summary, "delete_file")) {
		t.Fatalf("destructive command was not rejected: %#v", res)
	}
}

func TestRunCommandRejectsNetworkWhenDisabled(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: false, ShellAccess: true}
	tool := RunCommandTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{"command": "curl https://example.com"})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError || !strings.Contains(res.Summary, "network") {
		t.Fatalf("network command was not rejected: %#v", res)
	}
}

func TestRunCommandRejectsForeignAbsolutePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses a Windows absolute path")
	}
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true}
	tool := RunCommandTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{"command": `type C:\Windows\win.ini`})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError || (!strings.Contains(res.Summary, "outside") && !strings.Contains(res.Summary, "read_file")) {
		t.Fatalf("foreign path was not rejected: %#v", res)
	}
}

func TestRunCommandRedirectsFileOperationsToStructuredTools(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true, AllowDestructiveOps: true}
	tool := RunCommandTool{Policy: policy}
	for command, expected := range map[string]string{
		"Get-Content README.md":                       "read_file",
		`powershell -Command "Get-Content README.md"`: "read_file",
		"Get-ChildItem -Recurse":                      "list_dir",
		"rg TODO .":                                   "search",
		"Set-Content a.txt hello":                     "write_file",
		"Copy-Item a.txt b.txt":                       "copy_file",
		"Remove-Item obsolete.txt":                    "delete_file",
	} {
		args, _ := json.Marshal(map[string]string{"command": command})
		res, _ := tool.Execute(context.Background(), args)
		if !res.IsError || !strings.Contains(res.Summary, expected) {
			t.Fatalf("command %q was not redirected to %s: %#v", command, expected, res)
		}
	}
}

func TestPowerShellScriptWrittenByFileToolRunsWithUnicode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell encoding contract")
	}
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true, ShellAccess: true, MaxCommandSeconds: 30}
	write := WriteFileTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{
		"path":    "unicode.ps1",
		"content": "Write-Output '脚本编码正常'\n",
	})
	result, err := write.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("write result=%#v err=%v", result, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "unicode.ps1"))
	if err != nil || !strings.HasPrefix(string(raw), string(utf8BOM)) {
		t.Fatalf("PowerShell script must carry UTF-8 BOM: %x err=%v", raw, err)
	}
	run := RunCommandTool{Policy: policy}
	runArgs, _ := json.Marshal(map[string]string{"command": "powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File unicode.ps1"})
	runResult, err := run.Execute(context.Background(), runArgs)
	if err != nil || runResult.IsError || !strings.Contains(runResult.Parts[0].Output, "脚本编码正常") {
		t.Fatalf("run result=%#v err=%v", runResult, err)
	}
}
