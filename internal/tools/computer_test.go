package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
)

type fakeComputerController struct {
	action string
	id     string
	x      int
	y      int
}

func (f *fakeComputerController) ListWindows(context.Context) ([]computercontrol.Window, error) {
	f.action = "list_windows"
	return []computercontrol.Window{{ID: "0x123", Title: "Editor", ProcessName: "Code.exe"}}, nil
}
func (f *fakeComputerController) FocusWindow(_ context.Context, id string) error {
	f.action, f.id = "focus", id
	return nil
}
func (f *fakeComputerController) ClickWindow(_ context.Context, id string, x, y int) error {
	f.action, f.id, f.x, f.y = "click", id, x, y
	return nil
}
func (f *fakeComputerController) TypeText(_ context.Context, id, _ string) error {
	f.action, f.id = "type", id
	return nil
}
func (f *fakeComputerController) PressKey(_ context.Context, id, _ string, _, _, _ bool) error {
	f.action, f.id = "key", id
	return nil
}
func (f *fakeComputerController) ScreenshotWindow(_ context.Context, id, path string) (string, error) {
	f.action, f.id = "screenshot", id
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func TestComputerToolListsAndClicksWindows(t *testing.T) {
	controller := &fakeComputerController{}
	tool := ComputerTool{Policy: SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write"}, Controller: controller}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list_windows"}`))
	if err != nil || result.IsError || controller.action != "list_windows" || len(result.Parts) != 1 {
		t.Fatalf("list result=%+v err=%v", result, err)
	}
	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"click","window_id":"0x123","x":40,"y":50}`))
	if err != nil || result.IsError || controller.action != "click" || controller.x != 40 || controller.y != 50 {
		t.Fatalf("click result=%+v err=%v", result, err)
	}
}

func TestComputerToolScreenshotStaysInWorkspace(t *testing.T) {
	root := t.TempDir()
	controller := &fakeComputerController{}
	tool := ComputerTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}, Controller: controller}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","window_id":"0x123","path":"artifacts/window.png"}`))
	if err != nil || result.IsError || len(result.Parts) != 2 {
		t.Fatalf("screenshot result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts", "window.png")); err != nil {
		t.Fatal(err)
	}
	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","window_id":"0x123","path":"../outside.png"}`))
	if err != nil || !result.IsError {
		t.Fatalf("outside result=%+v err=%v", result, err)
	}
}
