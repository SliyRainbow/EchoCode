//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeResolvedSearchRootKeepsAbsoluteWindowsRoot(t *testing.T) {
	original := filepath.Clean(`C:\Users\runneradmin\AppData\Local\Temp\workspace`)
	resolved := filepath.Join("..", "..", "runneradmin", "AppData", "Local", "Temp", "workspace")
	if got := normalizeResolvedSearchRoot(original, resolved); got != original {
		t.Fatalf("normalized root = %q, want %q", got, original)
	}
}

func TestSearchRelativePathRejectsEscapingWindowsPaths(t *testing.T) {
	for _, relative := range []string{`..`, `..\outside\file.go`, `C:\outside\file.go`} {
		if searchRelativePathStaysWithinRoot(relative) {
			t.Fatalf("escaping relative path %q was accepted", relative)
		}
	}
	if !searchRelativePathStaysWithinRoot(filepath.Join("src", "文件.go")) {
		t.Fatal("workspace-relative path was rejected")
	}
}

func TestStructuredSearchWindowsUnicodeWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Windows 中文 项目")
	path := filepath.Join(root, "目录 含空格", "代码.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package demo\n// 中文命中\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}
	grep := GrepTool{
		Policy: policy,
		lookPath: func(string) (string, error) {
			return "", errors.New("force built-in engine")
		},
	}
	grepArguments, _ := json.Marshal(map[string]any{"query": "中文命中", "path": "目录 含空格"})
	grepResult, err := grep.Execute(context.Background(), grepArguments)
	if err != nil || grepResult.IsError {
		t.Fatalf("grep result=%#v err=%v", grepResult, err)
	}
	grepOutput := decodeGrepOutput(t, grepResult)
	if grepOutput.Count != 1 || grepOutput.Matches[0].Path != "目录 含空格/代码.go" {
		t.Fatalf("grep output = %#v", grepOutput)
	}

	globArguments, _ := json.Marshal(map[string]any{"pattern": "**/*.GO", "kind": "file"})
	globResult, err := (GlobTool{Policy: policy}).Execute(context.Background(), globArguments)
	if err != nil || globResult.IsError {
		t.Fatalf("glob result=%#v err=%v", globResult, err)
	}
	globOutput := decodeGlobOutput(t, globResult)
	if globOutput.Count != 1 || globOutput.Entries[0].Path != "目录 含空格/代码.go" {
		t.Fatalf("glob output = %#v", globOutput)
	}
}
