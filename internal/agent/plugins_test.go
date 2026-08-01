package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginapi "github.com/SliyRainbow/EchoCode/internal/plugins"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestPluginToolsRegisterAcrossAgentPlanWorkerAndSessionRuntime(t *testing.T) {
	pluginsDir := t.TempDir()
	workspace := t.TempDir()
	writeAgentTestPlugin(t, pluginsDir)
	service := NewService(ServiceConfig{
		SkillsDir:  t.TempDir(),
		PluginsDir: pluginsDir,
		AppVersion: "test-version",
	})
	settings := service.runtimeSettings
	settings.WorkspaceRoot = workspace
	settings.Plugins = pluginapi.Settings{
		MaxExecutionSeconds: 30,
		MaxOutputBytes:      1024 * 1024,
		Entries: []pluginapi.Setting{{
			ID:      "agent-integration",
			Enabled: true,
			Permissions: pluginapi.PermissionGrant{
				FileRead:  true,
				FileWrite: true,
			},
		}},
	}
	service.runtimeSettings = settings.Normalized()

	const readName = "plugin__agent-integration__inspect"
	const writeName = "plugin__agent-integration__write"
	assertRegistryContains(t, service.buildToolRegistry(), readName, writeName)
	scopedCtx := withTurnTaskScope(context.Background(), turnTaskScope{
		Enabled: true,
		Roots:   []string{filepath.Join(workspace, "target")},
	})
	assertRegistryContains(t, service.buildToolRegistryForContext(scopedCtx), readName, writeName)
	assertRegistryContains(t, service.buildWorkerToolRegistry(), readName, writeName)
	planRegistry := service.buildReadOnlyRegistry()
	assertRegistryContains(t, planRegistry, readName)
	if _, exists := planRegistry.Get(writeName); exists {
		t.Fatalf("Plan registry unexpectedly contains mutating plugin tool %q", writeName)
	}

	writeTool, exists := service.buildToolRegistry().Get(writeName)
	if !exists {
		t.Fatal("write plugin tool was not registered")
	}
	if !toolNeedsExclusiveWorkspaceAccess(writeName, writeTool) {
		t.Fatal("mutating plugin tool must serialize workspace access")
	}
	readTool, _ := service.buildToolRegistry().Get(readName)
	if toolNeedsExclusiveWorkspaceAccess(readName, readTool) {
		t.Fatal("read-only plugin tool should not take the workspace mutation lock")
	}
	result, err := service.runToolWithApproval(context.Background(), writeTool, writeName, json.RawMessage(`{"path":"report.txt"}`))
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "拒绝") {
		t.Fatalf("unattended write approval result = %#v, err = %v", result, err)
	}

	runtime, err := service.NewSessionRuntime("plugin-session")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.pluginManager != service.pluginManager {
		t.Fatal("session runtime did not share the process-wide plugin catalog")
	}
	assertRegistryContains(t, runtime.buildToolRegistry(), readName, writeName)
	if runtime.pluginManager.AppVersion() != "test-version" {
		t.Fatalf("plugin host version = %q", runtime.pluginManager.AppVersion())
	}
}

func assertRegistryContains(t *testing.T, registry *tools.Registry, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, exists := registry.Get(name); !exists {
			t.Fatalf("registry does not contain %q", name)
		}
	}
}

func writeAgentTestPlugin(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "agent-integration")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := pluginapi.Manifest{
		SchemaVersion: 1,
		ID:            "agent-integration",
		Name:          "Agent Integration",
		Version:       "1.0.0",
		Runtime:       pluginapi.Runtime{Transport: "stdio", Command: os.Args[0]},
		Permissions:   pluginapi.PermissionSpec{FileRead: true, FileWrite: true},
		Tools: []pluginapi.ToolManifest{
			{
				Name: "inspect", Description: "Inspect a file", ReadOnly: true,
				Permissions: pluginapi.PermissionSpec{FileRead: true},
				Paths:       []pluginapi.PathRequirement{{Argument: "path", Access: "read"}},
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
			},
			{
				Name: "write", Description: "Write a file",
				Permissions: pluginapi.PermissionSpec{FileWrite: true},
				Paths:       []pluginapi.PathRequirement{{Argument: "path", Access: "write"}},
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pluginapi.ManifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
