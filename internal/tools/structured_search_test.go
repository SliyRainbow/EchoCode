package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRegistryAddsStructuredSearchToolsInStableOrder(t *testing.T) {
	registry := NewRegistry()
	registry.AddStructuredSearch(SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "read-only"})

	if registry.Len() != 2 {
		t.Fatalf("registry length = %d, want 2", registry.Len())
	}
	for _, name := range []string{"grep", "glob"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("registry is missing %q", name)
		}
	}
	schemas := registry.Schemas()
	if schemas[0].Function.Name != "grep" || schemas[1].Function.Name != "glob" {
		t.Fatalf("schema order = %q, %q", schemas[0].Function.Name, schemas[1].Function.Name)
	}
}

func TestStableSearchPathHandlesResolvedWorkspaceAlias(t *testing.T) {
	parent := t.TempDir()
	realWorkspace := filepath.Join(parent, "real-workspace")
	aliasWorkspace := filepath.Join(parent, "workspace-alias")
	target := filepath.Join(realWorkspace, "src", "file.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realWorkspace, aliasWorkspace); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	if got := stableSearchPath(SandboxPolicy{WorkspaceRoot: aliasWorkspace}, target); got != "src/file.go" {
		t.Fatalf("stable path = %q, want %q", got, "src/file.go")
	}
}

func TestGrepUsesRipgrepAndParsesUnicodeColumns(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 工作区")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var capturedExecutable string
	var capturedArguments []string
	tool := GrepTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"},
		lookPath: func(name string) (string, error) {
			if name != "rg" {
				t.Fatalf("lookPath name = %q", name)
			}
			return "test-rg", nil
		},
		commandFactory: func(ctx context.Context, executable string, arguments ...string) *exec.Cmd {
			capturedExecutable = executable
			capturedArguments = append([]string(nil), arguments...)
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestRipgrepStructuredSearchHelper")
			command.Env = append(os.Environ(), "ECHOCODE_RG_HELPER=1")
			return command
		},
	}
	arguments, _ := json.Marshal(map[string]any{
		"query":          "needle",
		"path":           ".",
		"include_globs":  []string{"**/*.go"},
		"exclude_globs":  []string{"**/*_test.go"},
		"case_sensitive": true,
	})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGrepOutput(t, result)
	if output.Engine != "rg" || output.Count != 1 || len(output.Matches) != 1 {
		t.Fatalf("grep output = %#v", output)
	}
	match := output.Matches[0]
	if match.Path != "中文目录/文件.go" || match.Line != 7 || match.Column != 4 || match.Snippet != "前缀 needle 后缀" {
		t.Fatalf("match = %#v", match)
	}
	if capturedExecutable != "test-rg" {
		t.Fatalf("executable = %q", capturedExecutable)
	}
	for _, required := range []string{"--json", "--fixed-strings", "--regexp", "needle", "--", "."} {
		if !slices.Contains(capturedArguments, required) {
			t.Fatalf("ripgrep arguments %q do not contain %q", capturedArguments, required)
		}
	}
}

func TestGrepUsesInstalledRipgrepWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	root := filepath.Join(t.TempDir(), "真实 rg 中文目录")
	path := filepath.Join(root, "src", "真实.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package demo\n// real-needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := GrepTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	arguments, _ := json.Marshal(map[string]any{
		"query":         "real-needle",
		"include_globs": []string{"**/*.go"},
	})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGrepOutput(t, result)
	if output.Engine != "rg" || output.Count != 1 || output.Matches[0].Path != "src/真实.go" || output.Matches[0].Line != 2 {
		t.Fatalf("ripgrep output = %#v", output)
	}
}

func TestGrepRipgrepTruncatesGlobalResults(t *testing.T) {
	root := t.TempDir()
	tool := GrepTool{
		Policy:   SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"},
		lookPath: func(string) (string, error) { return "test-rg", nil },
		commandFactory: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestRipgrepStructuredSearchHelper")
			command.Env = append(os.Environ(), "ECHOCODE_RG_HELPER=1", "ECHOCODE_RG_MATCH_COUNT=3")
			return command
		},
	}
	arguments, _ := json.Marshal(map[string]any{"query": "needle", "max_results": 2})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGrepOutput(t, result)
	if output.Engine != "rg" || output.Count != 2 || !output.Truncated {
		t.Fatalf("ripgrep truncation output = %#v", output)
	}
}

func TestRipgrepStructuredSearchHelper(t *testing.T) {
	if os.Getenv("ECHOCODE_RG_HELPER") != "1" {
		return
	}
	matchCount, _ := strconv.Atoi(os.Getenv("ECHOCODE_RG_MATCH_COUNT"))
	if matchCount <= 0 {
		matchCount = 1
	}
	for index := 0; index < matchCount; index++ {
		path := "中文目录/文件.go"
		if matchCount > 1 {
			path = fmt.Sprintf("中文目录/文件%d.go", index+1)
		}
		event := map[string]any{
			"type": "match",
			"data": map[string]any{
				"path":        map[string]any{"text": path},
				"lines":       map[string]any{"text": "前缀 needle 后缀\n"},
				"line_number": 7,
				"submatches": []map[string]any{{
					"match": map[string]any{"text": "needle"},
					"start": len("前缀 "),
					"end":   len("前缀 needle"),
				}},
			},
		}
		encoded, _ := json.Marshal(event)
		_, _ = fmt.Fprintln(os.Stdout, string(encoded))
	}
	os.Exit(0)
}

func TestGrepFallsBackForUnicodePathsAndTruncates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 工作区")
	directory := filepath.Join(root, "源代码")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"一.go", "三.go", "二.go"} {
		content := "package demo\n// 目标 " + strings.Repeat("内容", 30) + "\n"
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "忽略.txt"), []byte("目标\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := GrepTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"},
		lookPath: func(string) (string, error) {
			return "", errors.New("rg unavailable")
		},
	}
	arguments, _ := json.Marshal(map[string]any{
		"query":             "目标",
		"path":              "源代码",
		"include_globs":     []string{"**/*.go"},
		"max_results":       2,
		"max_snippet_chars": 40,
	})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGrepOutput(t, result)
	if output.Engine != "go" || output.Count != 2 || !output.Truncated || output.TimedOut {
		t.Fatalf("grep output = %#v", output)
	}
	for _, match := range output.Matches {
		if !strings.HasPrefix(match.Path, "源代码/") || !strings.HasSuffix(match.Path, ".go") {
			t.Fatalf("unexpected match path %q", match.Path)
		}
		if match.Line != 2 || match.Column != 4 || !match.SnippetTruncated {
			t.Fatalf("unexpected match = %#v", match)
		}
	}
	if output.Matches[0].Path > output.Matches[1].Path {
		t.Fatalf("matches are not sorted: %#v", output.Matches)
	}
}

func TestGrepFallbackKeepsUnicodeColumnOffsets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "unicode.txt"), []byte("İ TARGET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := GrepTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"},
		lookPath: func(string) (string, error) {
			return "", errors.New("rg unavailable")
		},
	}
	arguments, _ := json.Marshal(map[string]any{"query": "target"})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGrepOutput(t, result)
	if output.Count != 1 || output.Matches[0].Column != 3 {
		t.Fatalf("unicode match output = %#v", output)
	}
}

func TestGlobSupportsGlobstarUnicodePathsAndTruncation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 工作区")
	for _, relative := range []string{
		"src/中文/一.go",
		"src/中文/二.go",
		"src/中文/三.go",
		"src/中文/忽略.txt",
		"node_modules/依赖.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := GlobTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	arguments, _ := json.Marshal(map[string]any{
		"pattern":     "**/*.go",
		"kind":        "file",
		"max_results": 2,
	})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	output := decodeGlobOutput(t, result)
	if output.Count != 2 || !output.Truncated || output.TimedOut {
		t.Fatalf("glob output = %#v", output)
	}
	for _, entry := range output.Entries {
		if !strings.HasPrefix(entry.Path, "src/中文/") || entry.Type != "file" {
			t.Fatalf("unexpected entry = %#v", entry)
		}
	}
}

func TestStructuredSearchRejectsEscapingPathsAndPatterns(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}

	grepArguments, _ := json.Marshal(map[string]any{"query": "x", "path": outside})
	grepResult, err := (GrepTool{Policy: policy}).Execute(context.Background(), grepArguments)
	if err != nil || !grepResult.IsError || !strings.Contains(grepResult.Summary, ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("escaping grep result=%#v err=%v", grepResult, err)
	}

	globArguments, _ := json.Marshal(map[string]any{"pattern": "../*.go"})
	globResult, err := (GlobTool{Policy: policy}).Execute(context.Background(), globArguments)
	if err != nil || !globResult.IsError || !strings.Contains(globResult.Summary, "parent directory") {
		t.Fatalf("escaping glob result=%#v err=%v", globResult, err)
	}
}

func TestStructuredSearchSkipsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-secret.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}
	grep := GrepTool{
		Policy: policy,
		lookPath: func(string) (string, error) {
			return "", errors.New("force built-in engine")
		},
	}
	grepArguments, _ := json.Marshal(map[string]any{"query": "outside-secret"})
	grepResult, err := grep.Execute(context.Background(), grepArguments)
	if err != nil || grepResult.IsError || decodeGrepOutput(t, grepResult).Count != 0 {
		t.Fatalf("symlink grep result=%#v err=%v", grepResult, err)
	}

	globArguments, _ := json.Marshal(map[string]any{"pattern": "**/*.txt"})
	globResult, err := (GlobTool{Policy: policy}).Execute(context.Background(), globArguments)
	if err != nil || globResult.IsError || decodeGlobOutput(t, globResult).Count != 0 {
		t.Fatalf("symlink glob result=%#v err=%v", globResult, err)
	}
}

func TestStructuredSearchReportsTimeoutMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expiredContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	grep := GrepTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"},
		lookPath: func(string) (string, error) {
			return "", errors.New("rg unavailable")
		},
	}
	grepArguments, _ := json.Marshal(map[string]any{"query": "needle"})
	grepResult, err := grep.Execute(expiredContext, grepArguments)
	if err != nil || !grepResult.IsError {
		t.Fatalf("grep result=%#v err=%v", grepResult, err)
	}
	grepOutput := decodeGrepOutput(t, grepResult)
	if !grepOutput.TimedOut || !grepOutput.Truncated || grepOutput.Error == "" {
		t.Fatalf("grep timeout output = %#v", grepOutput)
	}

	globArguments, _ := json.Marshal(map[string]any{"pattern": "**/*.go"})
	globResult, err := (GlobTool{Policy: grep.Policy}).Execute(expiredContext, globArguments)
	if err != nil || !globResult.IsError {
		t.Fatalf("glob result=%#v err=%v", globResult, err)
	}
	globOutput := decodeGlobOutput(t, globResult)
	if !globOutput.TimedOut || !globOutput.Truncated || globOutput.Error == "" {
		t.Fatalf("glob timeout output = %#v", globOutput)
	}
}

func decodeGrepOutput(t *testing.T, result Result) GrepOutput {
	t.Helper()
	if len(result.Parts) != 1 || result.Parts[0].Kind != PartToolCall || result.Parts[0].Name != "grep" {
		t.Fatalf("grep parts = %#v", result.Parts)
	}
	var output GrepOutput
	if err := json.Unmarshal([]byte(result.Parts[0].Output), &output); err != nil {
		t.Fatalf("decode grep output: %v; payload=%q", err, result.Parts[0].Output)
	}
	return output
}

func decodeGlobOutput(t *testing.T, result Result) GlobOutput {
	t.Helper()
	if len(result.Parts) != 1 || result.Parts[0].Kind != PartToolCall || result.Parts[0].Name != "glob" {
		t.Fatalf("glob parts = %#v", result.Parts)
	}
	var output GlobOutput
	if err := json.Unmarshal([]byte(result.Parts[0].Output), &output); err != nil {
		t.Fatalf("decode glob output: %v; payload=%q", err, result.Parts[0].Output)
	}
	return output
}
