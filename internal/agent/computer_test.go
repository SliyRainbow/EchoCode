package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
)

type computerControllerStub struct{}

func (computerControllerStub) ListWindows(context.Context) ([]computercontrol.Window, error) {
	return nil, nil
}
func (computerControllerStub) FocusWindow(context.Context, string) error { return nil }
func (computerControllerStub) ClickWindow(context.Context, string, int, int) error {
	return nil
}
func (computerControllerStub) TypeText(context.Context, string, string) error { return nil }
func (computerControllerStub) PressKey(context.Context, string, string, bool, bool, bool) error {
	return nil
}
func (computerControllerStub) ScreenshotWindow(context.Context, string, string) (string, error) {
	return "", nil
}

func TestToolRegistryIncludesComputerOnlyWhenEnabled(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), Computer: computerControllerStub{}})
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()

	if _, ok := svc.buildToolRegistry().Get("computer"); ok {
		t.Fatal("disabled computer control must not expose the computer tool")
	}
	svc.runtimeSettings.ComputerControl.ChromeEnabled = true
	if _, ok := svc.buildToolRegistry().Get("computer"); !ok {
		t.Fatal("enabled computer control must expose the computer tool")
	}
	if _, ok := svc.buildReadOnlyRegistry().Get("computer"); ok {
		t.Fatal("plan registry must not expose the mutating computer tool")
	}
}

func TestComputerActionApproval(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "on-request"
	tests := []struct {
		name string
		args string
		want bool
	}{
		{name: "list windows", args: `{"action":"list_windows"}`, want: false},
		{name: "focus", args: `{"action":"focus","window_id":"0x123"}`, want: true},
		{name: "screenshot", args: `{"action":"screenshot","window_id":"0x123"}`, want: true},
		{name: "click", args: `{"action":"click","window_id":"0x123"}`, want: true},
		{name: "type", args: `{"action":"type","window_id":"0x123"}`, want: true},
		{name: "key", args: `{"action":"key","window_id":"0x123"}`, want: true},
		{name: "unknown", args: `{"action":"dance"}`, want: true},
		{name: "malformed", args: `{`, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := svc.needsApproval("computer", json.RawMessage(test.args)); got != test.want {
				t.Fatalf("needsApproval() = %v, want %v", got, test.want)
			}
		})
	}

	svc.runtimeSettings.ApprovalPolicy = "never"
	if svc.needsApproval("computer", json.RawMessage(`{"action":"click"}`)) {
		t.Fatal("never policy should not request interactive approval")
	}
}
