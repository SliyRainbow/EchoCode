package main

import (
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/extensions"
)

func TestExtensionMCPServerSettingResolvesInstallPaths(t *testing.T) {
	t.Parallel()
	installDir := filepath.Join(t.TempDir(), "CodeGraph")
	executable := filepath.Join(installDir, "bin", "codegraph.cmd")
	result := extensions.InstallResult{
		Package: extensions.InstalledPackage{ID: "mcp.codegraph", Name: "CodeGraph", InstallDir: installDir, Executable: executable},
		Manifest: extensions.Manifest{MCP: &extensions.MCPConfig{
			Transport:        "stdio",
			Command:          "${artifactExecutable}",
			Args:             []string{"serve", "--mcp", "${installDir}"},
			WorkingDirectory: "${workspaceRoot}",
			ToolResultPolicy: "summary-first",
			Env:              []extensions.KeyValue{{Key: "CODEGRAPH_HOME", Value: "${installDir}"}},
		}},
	}
	workspace := filepath.Join(t.TempDir(), "project")
	setting, err := extensionMCPServerSetting(result, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Command != executable || setting.WorkingDirectory != workspace {
		t.Fatalf("setting paths = %#v", setting)
	}
	if len(setting.Env) != 1 || setting.Env[0].Value != installDir {
		t.Fatalf("setting env = %#v", setting.Env)
	}
}

func TestUpsertExtensionMCPServerPreservesUserState(t *testing.T) {
	t.Parallel()
	servers := []agent.MCPServerSetting{{
		ID:                 "mcp.codegraph",
		Name:               "Old",
		Command:            "old.cmd",
		Enabled:            false,
		ToolResultPolicy:   "raw-local",
		SchemaSnapshotHash: "hash",
		LastSnapshotAt:     "now",
	}}
	next := agent.MCPServerSetting{ID: "mcp.codegraph", Name: "CodeGraph", Command: "new.cmd", Enabled: true, ToolResultPolicy: "summary-first"}
	result := upsertExtensionMCPServer(servers, next)
	if len(result) != 1 || result[0].Command != "new.cmd" || result[0].Enabled || result[0].ToolResultPolicy != "raw-local" {
		t.Fatalf("upsert result = %#v", result)
	}
	if result[0].SchemaSnapshotHash != "hash" || result[0].LastSnapshotAt != "now" {
		t.Fatalf("snapshot state was lost: %#v", result[0])
	}
}

func TestRemoveOwnedExtensionMCPServerKeepsUnrelatedCollision(t *testing.T) {
	t.Parallel()
	installed := extensions.InstalledPackage{ID: "mcp.codegraph", Executable: filepath.Join(t.TempDir(), "codegraph.cmd")}
	servers := []agent.MCPServerSetting{
		{ID: installed.ID, Command: installed.Executable},
		{ID: installed.ID, Command: filepath.Join(t.TempDir(), "custom.cmd")},
		{ID: "other", Command: "other"},
	}
	result := removeOwnedExtensionMCPServer(servers, installed)
	if len(result) != 2 || result[0].ID != installed.ID || result[1].ID != "other" {
		t.Fatalf("remove result = %#v", result)
	}
}
