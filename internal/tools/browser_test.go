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

type fakeBrowserController struct {
	action   string
	value    string
	selector string
	text     string
	err      error
}

func (f *fakeBrowserController) OpenURL(_ context.Context, value string) (string, error) {
	f.action, f.value = "open", value
	if f.err != nil {
		return "", f.err
	}
	return `{"tabId":"tab-1"}`, nil
}
func (f *fakeBrowserController) SnapshotJSON(context.Context) (string, error) {
	f.action = "snapshot"
	if f.err != nil {
		return "", f.err
	}
	return `{"title":"EchoCode"}`, nil
}
func (f *fakeBrowserController) ClickSelector(_ context.Context, selector string) error {
	f.action, f.selector = "click", selector
	return f.err
}
func (f *fakeBrowserController) TypeSelector(_ context.Context, selector, text string) error {
	f.action, f.selector, f.text = "type", selector, text
	return f.err
}
func (f *fakeBrowserController) PressKey(_ context.Context, key string) error {
	f.action, f.value = "key", key
	return f.err
}
func (f *fakeBrowserController) Back(context.Context) error    { f.action = "back"; return f.err }
func (f *fakeBrowserController) Forward(context.Context) error { f.action = "forward"; return f.err }
func (f *fakeBrowserController) Reload(context.Context) error  { f.action = "reload"; return f.err }
func (f *fakeBrowserController) Scroll(_ context.Context, _, _ float64) error {
	f.action = "scroll"
	return f.err
}
func (f *fakeBrowserController) CloseTab(context.Context) error { f.action = "close_tab"; return f.err }
func (f *fakeBrowserController) SaveScreenshot(_ context.Context, path string) (string, error) {
	f.action, f.value = "screenshot", path
	if f.err != nil {
		return "", f.err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func TestBrowserToolActions(t *testing.T) {
	root := t.TempDir()
	controller := &fakeBrowserController{}
	tool := BrowserTool{
		Policy:     SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Controller: controller,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"open","url":"https://example.com"}`))
	if err != nil || result.IsError || controller.action != "open" {
		t.Fatalf("open result=%+v err=%v action=%s", result, err, controller.action)
	}
	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"type","selector":"#query","text":"EchoCode"}`))
	if err != nil || result.IsError || controller.selector != "#query" || controller.text != "EchoCode" {
		t.Fatalf("type result=%+v err=%v", result, err)
	}
	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","path":"artifacts/page.png"}`))
	if err != nil || result.IsError {
		t.Fatalf("screenshot result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts", "page.png")); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserToolRejectsScreenshotOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	tool := BrowserTool{
		Policy:     SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Controller: &fakeBrowserController{},
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","path":"../outside.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("outside screenshot path should be rejected")
	}
}

func TestBrowserToolFailureIncludesActionAndRecoveryHint(t *testing.T) {
	tool := BrowserTool{
		Policy:     SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write"},
		Controller: &fakeBrowserController{err: errors.New("浏览标签页不存在")},
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"snapshot"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Parts) != 1 || result.Parts[0].Kind != PartToolCall {
		t.Fatalf("failure result=%+v", result)
	}
	if !strings.Contains(result.Parts[0].Output, "browser snapshot 执行失败") || !strings.Contains(result.Parts[0].Output, "browser open") {
		t.Fatalf("failure output=%q", result.Parts[0].Output)
	}
}
