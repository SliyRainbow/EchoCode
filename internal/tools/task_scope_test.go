package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskScopeAllowsReferenceReadsButRejectsUnrelatedWrites(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "echocode-agent-web-test")
	unrelated := filepath.Join(workspace, "existing-project")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "admin.css"), []byte("unrelated-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("root-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{
		WorkspaceRoot:    workspace,
		FilesystemAccess: "workspace-write",
		TaskScopeEnabled: true,
		TaskScopeRoots:   []string{target},
	}

	if _, err := policy.ResolveReadPath(filepath.Join(unrelated, "admin.css")); err != nil {
		t.Fatalf("authorized workspace reference read failed: %v", err)
	}
	if _, err := policy.ResolveReadPath(filepath.Join(workspace, "index.html")); err != nil {
		t.Fatalf("authorized workspace root read failed: %v", err)
	}
	if _, err := policy.ResolveWritePath(filepath.Join(workspace, "index.html")); !errors.Is(err, ErrPathOutsideTaskScope) {
		t.Fatalf("root-level unrelated write error = %v", err)
	}
	if resolved, err := policy.ResolveWritePath(filepath.Join(target, "index.html")); err != nil || resolved != filepath.Join(target, "index.html") {
		t.Fatalf("target write resolved=%q err=%v", resolved, err)
	}
}

func TestTaskScopeAllowsCreatingMissingTargetDirectory(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "echocode-agent-web-test")
	tool := WriteFileTool{Policy: SandboxPolicy{
		WorkspaceRoot:    workspace,
		FilesystemAccess: "workspace-write",
		TaskScopeEnabled: true,
		TaskScopeRoots:   []string{target},
	}}
	arguments, _ := json.Marshal(map[string]any{
		"path":    "echocode-agent-web-test/index.html",
		"content": "<!doctype html><title>Scoped</title>",
	})
	result, err := tool.Execute(context.Background(), arguments)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	content, err := os.ReadFile(filepath.Join(target, "index.html"))
	if err != nil || !strings.Contains(string(content), "Scoped") {
		t.Fatalf("created content=%q err=%v", content, err)
	}
}

func TestTaskScopeDirectoryIncludesDescendantsButFileGrantIsExact(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "exports")
	file := filepath.Join(workspace, "README.md")
	policy := SandboxPolicy{
		WorkspaceRoot: workspace, FilesystemAccess: "workspace-write",
		TaskScopeEnabled: true, TaskScopeRoots: []string{directory}, TaskScopeFiles: []string{file},
	}
	if _, err := policy.ResolveWritePath(filepath.Join(directory, "nested", "report.txt")); err != nil {
		t.Fatalf("directory descendant was rejected: %v", err)
	}
	if _, err := policy.ResolveWritePath(file); err != nil {
		t.Fatalf("exact file grant was rejected: %v", err)
	}
	if _, err := policy.ResolveWritePath(filepath.Join(workspace, "README.md.bak")); !errors.Is(err, ErrPathOutsideTaskScope) {
		t.Fatalf("file grant unexpectedly included a sibling path: %v", err)
	}
}

func TestTaskScopeAllowsOnlyAncestorsWhenCreatingTarget(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "nested", "echocode-agent-web-test")
	policy := SandboxPolicy{
		WorkspaceRoot: workspace, FilesystemAccess: "workspace-write",
		TaskScopeEnabled: true, TaskScopeRoots: []string{target},
	}
	if resolved, err := policy.ResolveCreateParentPath(filepath.Join(workspace, "nested")); err != nil || resolved != filepath.Join(workspace, "nested") {
		t.Fatalf("target ancestor rejected: %q err=%v", resolved, err)
	}
	if _, err := policy.ResolveCreateParentPath(filepath.Join(workspace, "existing-project")); !errors.Is(err, ErrPathOutsideTaskScope) {
		t.Fatalf("unrelated directory was accepted as a creation parent: %v", err)
	}
}

func TestTaskScopeDirectoryAndSearchToolsCanReadWorkspaceReferences(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "echocode-agent-web-test")
	unrelated := filepath.Join(workspace, "existing-project")
	for _, directory := range []string{target, unrelated} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "index.html"), []byte("scoped-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "admin.css"), []byte("unrelated-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{
		WorkspaceRoot:    workspace,
		FilesystemAccess: "read-only",
		TaskScopeEnabled: true,
		TaskScopeRoots:   []string{target},
	}

	listArguments, _ := json.Marshal(map[string]any{"path": ".", "max_depth": 4})
	listed, err := (ListDirTool{Policy: policy}).Execute(context.Background(), listArguments)
	if err != nil || listed.IsError {
		t.Fatalf("list result=%#v err=%v", listed, err)
	}
	listOutput := listed.Parts[0].Output
	if !strings.Contains(listOutput, "echocode-agent-web-test/index.html") || !strings.Contains(listOutput, "existing-project") || !strings.Contains(listOutput, "admin.css") {
		t.Fatalf("scoped list output = %q", listOutput)
	}

	searchArguments, _ := json.Marshal(map[string]any{"query": "marker", "path": "."})
	searched, err := (SearchTool{Policy: policy}).Execute(context.Background(), searchArguments)
	if err != nil || searched.IsError {
		t.Fatalf("search result=%#v err=%v", searched, err)
	}
	if !strings.Contains(searched.Parts[0].Output, "echocode-agent-web-test/index.html") || !strings.Contains(searched.Parts[0].Output, "admin.css") {
		t.Fatalf("scoped search output = %q", searched.Parts[0].Output)
	}

	grep := GrepTool{Policy: policy, lookPath: func(string) (string, error) {
		return "", errors.New("force built-in search")
	}}
	grepped, err := grep.Execute(context.Background(), searchArguments)
	if err != nil || grepped.IsError {
		t.Fatalf("grep result=%#v err=%v", grepped, err)
	}
	grepOutput := decodeGrepOutput(t, grepped)
	if grepOutput.Engine != "go" || grepOutput.Count != 2 {
		t.Fatalf("scoped grep output = %#v", grepOutput)
	}

	globArguments, _ := json.Marshal(map[string]any{"pattern": "**/*", "path": ".", "kind": "file"})
	globbed, err := (GlobTool{Policy: policy}).Execute(context.Background(), globArguments)
	if err != nil || globbed.IsError {
		t.Fatalf("glob result=%#v err=%v", globbed, err)
	}
	globOutput := decodeGlobOutput(t, globbed)
	if globOutput.Count != 2 {
		t.Fatalf("scoped glob output = %#v", globOutput)
	}
}
