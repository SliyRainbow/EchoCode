package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestPluginToolValidatesAndResolvesPaths(t *testing.T) {
	workspace := t.TempDir()
	descriptor := ToolManifest{
		Name:        "write",
		Description: "write",
		InputSchema: map[string]any{"type": "object"},
		Permissions: PermissionSpec{FileWrite: true},
		Paths:       []PathRequirement{{Argument: "path", Access: "write"}},
	}
	tool := &pluginTool{
		record:     record{manifest: Manifest{ID: "test-plugin", Name: "test"}},
		setting:    Setting{Permissions: PermissionGrant{FileWrite: true}},
		descriptor: descriptor,
		policy:     tools.SandboxPolicy{WorkspaceRoot: workspace, FilesystemAccess: "workspace-write"},
	}
	args := map[string]any{"path": "output.txt"}
	if err := tool.resolvePaths(args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != filepath.Join(workspace, "output.txt") {
		t.Fatalf("resolved path = %q", args["path"])
	}
	if err := tool.resolvePaths(map[string]any{"path": "../outside.txt"}); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestPluginToolRejectsNetworkWhenHostNetworkIsDisabled(t *testing.T) {
	tool := &pluginTool{
		record:  record{manifest: Manifest{ID: "test-plugin", Name: "test"}},
		setting: Setting{Permissions: PermissionGrant{Network: true}},
		descriptor: ToolManifest{
			Name: "network", Description: "network", InputSchema: map[string]any{"type": "object"}, Permissions: PermissionSpec{Network: true},
		},
		policy: tools.SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: false},
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestPluginToolEmitsWaitingBeforeRuntimeCall(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "success")
	tool := &pluginTool{
		manager:    &Manager{appVersion: "test"},
		record:     record,
		setting:    Setting{Permissions: PermissionGrant{FileRead: true}},
		descriptor: descriptor,
		policy:     testPluginPolicy(t.TempDir()),
		limits:     runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	}
	progress := make([]tools.ResultPart, 0, 1)
	ctx := tools.WithProgressSink(context.Background(), func(part tools.ResultPart) {
		progress = append(progress, part)
	})
	result, err := tool.Execute(ctx, json.RawMessage(`{"value":"hello"}`))
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(progress) == 0 || progress[0].Status != "waiting" || !strings.Contains(progress[0].Output, "插件运行时") {
		t.Fatalf("plugin progress = %#v", progress)
	}
}

func TestPluginToolPublishesValidatedWritePathsAsFileArtifacts(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "reports", "summary.docx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &pluginTool{
		descriptor: ToolManifest{Paths: []PathRequirement{{Argument: "path", Access: "write"}}},
		policy:     tools.SandboxPolicy{WorkspaceRoot: workspace, FilesystemAccess: "workspace-write"},
	}
	key := strings.ToLower(filepath.Clean(path))
	parts := tool.outputFileParts(map[string]any{"path": path}, map[string]bool{key: false})
	if len(parts) != 1 || parts[0].Kind != tools.PartFile || parts[0].Path != "reports/summary.docx" || parts[0].FileAction != "created" || !parts[0].Created {
		t.Fatalf("parts = %#v", parts)
	}
	outside := filepath.Join(t.TempDir(), "outside.docx")
	if err := os.WriteFile(outside, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if parts := tool.outputFileParts(map[string]any{"path": outside}, nil); len(parts) != 0 {
		t.Fatalf("outside artifact escaped policy: %#v", parts)
	}
}

func TestAccessReadOnlySQLClassifier(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("classifier is compiled with the Windows Access implementation")
	}
	// Kept here to make the platform expectation explicit. Windows-specific
	// classifier cases are covered by builtin_windows_test.go.
}
