package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGitRepositoryToolClonesAndPullsWithoutShellAccess(t *testing.T) {
	requireGitExecutable(t)
	root := t.TempDir()
	source := filepath.Join(root, "源 仓库")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, source, "init")
	runGitTestCommand(t, source, "branch", "-M", "main")
	writeGitTestFile(t, filepath.Join(source, "版本.txt"), "one")
	runGitTestCommand(t, source, "add", ".")
	runGitTestCommand(t, source, "-c", "user.name=EchoCode Test", "-c", "user.email=echocode@example.invalid", "commit", "-m", "initial")

	destination := filepath.Join(root, "下载 目录", "示例项目")
	tool := GitRepositoryTool{Policy: SandboxPolicy{
		WorkspaceRoot: root, FilesystemAccess: "workspace-write",
		NetworkAccess: true, ShellAccess: false,
	}}
	cloneArgs, _ := json.Marshal(gitRepositoryArguments{
		Action: "clone", URL: source, Destination: destination, Branch: "main",
	})
	var mu sync.Mutex
	var progress []ResultPart
	ctx := WithProgressSink(context.Background(), func(part ResultPart) {
		mu.Lock()
		progress = append(progress, part)
		mu.Unlock()
	})
	cloneResult, err := tool.Execute(ctx, cloneArgs)
	if err != nil || cloneResult.IsError {
		t.Fatalf("clone result=%#v err=%v", cloneResult, err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "版本.txt"))
	if err != nil || string(content) != "one" {
		t.Fatalf("cloned content=%q err=%v", content, err)
	}
	if len(cloneResult.Parts) != 1 || cloneResult.Parts[0].ExitCode == nil || *cloneResult.Parts[0].ExitCode != 0 {
		t.Fatalf("clone parts=%#v", cloneResult.Parts)
	}
	mu.Lock()
	progressSnapshot := append([]ResultPart(nil), progress...)
	mu.Unlock()
	if len(progressSnapshot) == 0 || progressSnapshot[0].Name != tool.Name() || progressSnapshot[0].Status != "running" {
		t.Fatalf("clone progress=%#v", progressSnapshot)
	}

	writeGitTestFile(t, filepath.Join(source, "版本.txt"), "two")
	runGitTestCommand(t, source, "add", ".")
	runGitTestCommand(t, source, "-c", "user.name=EchoCode Test", "-c", "user.email=echocode@example.invalid", "commit", "-m", "second")
	pullArgs, _ := json.Marshal(gitRepositoryArguments{
		Action: "pull", Repository: destination, Remote: "origin", Branch: "main", Strategy: "ff-only",
	})
	pullResult, err := tool.Execute(context.Background(), pullArgs)
	if err != nil || pullResult.IsError {
		t.Fatalf("pull result=%#v err=%v", pullResult, err)
	}
	content, err = os.ReadFile(filepath.Join(destination, "版本.txt"))
	if err != nil || string(content) != "two" {
		t.Fatalf("pulled content=%q err=%v", content, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".echocode-clone-*")); len(matches) != 0 {
		t.Fatalf("temporary clone directories remain: %v", matches)
	}
}

func TestGitRepositoryToolRejectsOutsideDestinationAndRedactsURL(t *testing.T) {
	requireGitExecutable(t)
	root := t.TempDir()
	tool := GitRepositoryTool{Policy: SandboxPolicy{
		WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true,
	}}
	outside := filepath.Join(t.TempDir(), "outside")
	rawArgs, _ := json.Marshal(gitRepositoryArguments{
		Action: "clone", URL: "https://user:secret@example.com/acme/demo.git?token=hidden", Destination: outside,
	})
	result, err := tool.Execute(context.Background(), rawArgs)
	if err != nil || !result.IsError || !strings.Contains(result.Summary, ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("outside result=%#v err=%v", result, err)
	}
	display := GitRepositoryInputForDisplay(rawArgs)
	if strings.Contains(display, "secret") || strings.Contains(display, "hidden") || !strings.Contains(display, "https://example.com/acme/demo.git") {
		t.Fatalf("unsafe display=%q", display)
	}
}

func TestGitRepositoryOutputRedactsRemoteCredentials(t *testing.T) {
	rawURL := "https://user:password@example.com/acme/demo.git?token=temporary-secret"
	output := sanitizeGitRepositoryOutput("fatal: unable to access '"+rawURL+"'; redirect https://other:credential@cdn.example.com/demo.git?signature=hidden", rawURL)
	for _, secret := range []string{"user", "password", "temporary-secret", "credential", "hidden"} {
		if strings.Contains(output, secret) {
			t.Fatalf("Git output leaked %q: %s", secret, output)
		}
	}
	for _, expected := range []string{"https://example.com/acme/demo.git", "https://cdn.example.com/demo.git"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Git output lost safe URL %q: %s", expected, output)
		}
	}
}

func TestGitRepositoryToolCancelledCloneCleansTemporaryDirectory(t *testing.T) {
	requireGitExecutable(t)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, source, "init")
	destination := filepath.Join(root, "cancelled")
	tool := GitRepositoryTool{Policy: SandboxPolicy{
		WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true,
	}}
	rawArgs, _ := json.Marshal(gitRepositoryArguments{Action: "clone", URL: source, Destination: destination})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := tool.Execute(ctx, rawArgs)
	if err != nil || !result.IsError {
		t.Fatalf("cancelled result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled destination exists: %v", statErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".echocode-clone-*")); len(matches) != 0 {
		t.Fatalf("cancelled clone left temporary directories: %v", matches)
	}
}

func TestGitRepositoryToolRetriesTransientCloneWithFreshTemporaryDirectory(t *testing.T) {
	requireGitExecutable(t)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	counterPath := filepath.Join(root, "attempts.txt")
	destination := filepath.Join(root, "中文 目录", "repository")
	tool := GitRepositoryTool{
		Policy: SandboxPolicy{
			WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true,
		},
		RetryDelay: time.Millisecond,
		commandFactory: func(ctx context.Context, args []string) *exec.Cmd {
			helperArgs := []string{
				"-test.run=^TestGitRepositoryRetryHelperProcess$", "--",
				"__git_retry__", counterPath,
			}
			helperArgs = append(helperArgs, args...)
			return exec.CommandContext(ctx, executable, helperArgs...)
		},
	}
	rawArgs, _ := json.Marshal(gitRepositoryArguments{
		Action: "clone", URL: "https://github.com/example/repository.git",
		Destination: destination, Depth: 1,
	})
	var mu sync.Mutex
	var progress []ResultPart
	ctx := WithProgressSink(context.Background(), func(part ResultPart) {
		mu.Lock()
		progress = append(progress, part)
		mu.Unlock()
	})
	result, err := tool.Execute(ctx, rawArgs)
	if err != nil || result.IsError {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	countRaw, err := os.ReadFile(counterPath)
	if err != nil || strings.TrimSpace(string(countRaw)) != "2" {
		t.Fatalf("attempt count=%q err=%v", countRaw, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README.md")); err != nil {
		t.Fatalf("retried clone was not installed: %v", err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Attempt != 2 || !strings.Contains(result.Parts[0].Output, "Retry history") {
		t.Fatalf("retry result parts=%#v", result.Parts)
	}
	mu.Lock()
	updates := append([]ResultPart(nil), progress...)
	mu.Unlock()
	foundRetry := false
	for _, update := range updates {
		if update.Status == "retrying" && update.Attempt == 2 {
			foundRetry = true
			break
		}
	}
	if !foundRetry {
		t.Fatalf("retry progress missing: %#v", updates)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".echocode-clone-*")); len(matches) != 0 {
		t.Fatalf("temporary clone directories remain: %v", matches)
	}
}

func TestGitRepositoryRetryHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+3 >= len(os.Args) || os.Args[separator+1] != "__git_retry__" {
		return
	}
	arguments := os.Args[separator+2:]
	counterPath := arguments[0]
	gitArguments := arguments[1:]
	count := 0
	if raw, err := os.ReadFile(counterPath); err == nil {
		count, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	count++
	if err := os.WriteFile(counterPath, []byte(strconv.Itoa(count)), 0o600); err != nil {
		os.Exit(2)
	}
	if count == 1 {
		_, _ = fmt.Fprintln(os.Stderr, "fatal: unable to access repository: schannel: failed to receive handshake")
		os.Exit(1)
	}
	if len(gitArguments) == 0 {
		os.Exit(3)
	}
	destination := gitArguments[len(gitArguments)-1]
	if err := os.MkdirAll(filepath.Join(destination, ".git"), 0o755); err != nil {
		os.Exit(4)
	}
	if err := os.WriteFile(filepath.Join(destination, "README.md"), []byte("retried\n"), 0o600); err != nil {
		os.Exit(5)
	}
}

func requireGitExecutable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git executable is unavailable")
	}
}

func runGitTestCommand(t *testing.T, directory string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeGitTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
