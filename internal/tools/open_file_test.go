package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFileToolUsesValidatedAbsolutePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "preview.html")
	if err := os.WriteFile(file, []byte("<h1>preview</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened := ""
	tool := OpenFileTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Open: func(path string) error {
			opened = path
			return nil
		},
	}
	args, _ := json.Marshal(map[string]string{"path": "preview.html"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("open_file failed: result=%+v err=%v", result, err)
	}
	if opened != file {
		t.Fatalf("opened = %q, want %q", opened, file)
	}
	if len(result.Parts) != 2 || result.Parts[0].Kind != PartToolCall || result.Parts[1].Kind != PartFile {
		t.Fatalf("open_file parts = %+v", result.Parts)
	}
	if result.Parts[1].FileAction != "available" {
		t.Fatalf("file action = %q", result.Parts[1].FileAction)
	}
}

func TestOpenFileToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	tool := OpenFileTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Open: func(string) error {
			called = true
			return nil
		},
	}
	args, _ := json.Marshal(map[string]string{"path": outside})
	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError || called {
		t.Fatalf("outside workspace result=%+v called=%v", result, called)
	}
}

func TestOpenFileToolUsesBuiltInPreviewForHTML(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "preview.html")
	if err := os.WriteFile(file, []byte("<h1>preview</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened, previewed := false, ""
	tool := OpenFileTool{
		Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Open: func(string) error {
			opened = true
			return nil
		},
		Preview: func(path string) error {
			previewed = path
			return nil
		},
	}
	args, _ := json.Marshal(map[string]string{"path": "preview.html"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("open_file failed: result=%+v err=%v", result, err)
	}
	if opened || previewed != file {
		t.Fatalf("opened=%v previewed=%q want=%q", opened, previewed, file)
	}
	if result.Parts[0].Output != "EchoCode 内置浏览器已打开预览" {
		t.Fatalf("tool output = %q", result.Parts[0].Output)
	}
}
