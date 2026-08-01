package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestManagerInstallDefaultsToUnconfiguredAndUninstall(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	manifest := testManifest()
	manifest.Runtime.Command = "bin/plugin"
	writeManifest(t, source, manifest)
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin", "plugin"), []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(root, "0.0.1")
	installed, err := manager.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if installed.ID != manifest.ID {
		t.Fatalf("installed = %#v", installed)
	}
	statuses := manager.Statuses(DefaultSettings())
	status := statusByID(statuses, manifest.ID)
	if status.State != "disabled" || !status.CanUninstall {
		t.Fatalf("status = %#v", status)
	}
	settings := UpsertSetting(DefaultSettings(), Setting{ID: manifest.ID, Enabled: true, Permissions: PermissionGrant{FileRead: true}})
	status = statusByID(manager.Statuses(settings), manifest.ID)
	if status.State != "ready" || status.AvailableToolCount != 1 {
		t.Fatalf("enabled status = %#v", status)
	}
	policy := tools.SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write"}
	pluginTools := manager.Tools(settings, policy, false)
	if len(pluginTools) < 1 || pluginTools[len(pluginTools)-1].Name() != "plugin__test-plugin__read" {
		t.Fatalf("tools = %#v", pluginTools)
	}
	policy.TaskScopeEnabled = true
	policy.TaskScopeRoots = []string{filepath.Join(policy.WorkspaceRoot, "target")}
	for _, scopedTool := range manager.Tools(settings, policy, false) {
		if scopedTool.Name() == "plugin__test-plugin__read" {
			t.Fatalf("plugin without declared path arguments leaked into scoped registry: %#v", scopedTool)
		}
	}
	if err := manager.Uninstall(manifest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, manifest.ID)); !os.IsNotExist(err) {
		t.Fatalf("plugin directory still exists: %v", err)
	}
}

func TestPluginToolScopeRequiresDeclaredFilesystemPaths(t *testing.T) {
	for name, test := range map[string]struct {
		descriptor ToolManifest
		want       bool
	}{
		"network-only": {
			descriptor: ToolManifest{Permissions: PermissionSpec{Network: true}},
			want:       true,
		},
		"undeclared-read": {
			descriptor: ToolManifest{Permissions: PermissionSpec{FileRead: true}},
		},
		"declared-read": {
			descriptor: ToolManifest{
				Permissions: PermissionSpec{FileRead: true},
				Paths:       []PathRequirement{{Argument: "path", Access: "read"}},
			},
			want: true,
		},
		"missing-write-path": {
			descriptor: ToolManifest{
				Permissions: PermissionSpec{FileRead: true, FileWrite: true},
				Paths:       []PathRequirement{{Argument: "source", Access: "read"}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := pluginToolDeclaresScopedPaths(test.descriptor); got != test.want {
				t.Fatalf("compatible=%v want=%v", got, test.want)
			}
		})
	}
}

func TestManagerRejectsSymlinkPackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links may require Windows developer mode")
	}
	source := t.TempDir()
	writeManifest(t, source, testManifest())
	if err := os.Symlink(filepath.Join(source, ManifestFileName), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(t.TempDir(), "test")
	if _, err := manager.Install(source); err == nil {
		t.Fatal("expected symbolic link package to be rejected")
	}
}

func TestManagerRefreshMarksCrossPluginToolNameCollisions(t *testing.T) {
	root := t.TempDir()
	first := testManifest()
	first.ID = "reports.reader"
	first.Name = "Reports Reader One"
	second := testManifest()
	second.ID = "reports_reader"
	second.Name = "Reports Reader Two"
	for _, manifest := range []Manifest{first, second} {
		dir := filepath.Join(root, manifest.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeManifest(t, dir, manifest)
	}

	manager := NewManager(root, "test")
	for _, id := range []string{first.ID, second.ID} {
		status := statusByID(manager.Statuses(DefaultSettings()), id)
		if status.State != "error" {
			t.Fatalf("status for %s = %#v", id, status)
		}
		if !strings.Contains(status.Message, "plugin__reports_reader__read") ||
			!strings.Contains(status.Message, first.ID) || !strings.Contains(status.Message, second.ID) {
			t.Fatalf("collision message for %s = %q", id, status.Message)
		}
	}
	for _, pluginTool := range manager.Tools(DefaultSettings(), tools.SandboxPolicy{}, false) {
		if pluginTool.Name() == "plugin__reports_reader__read" {
			t.Fatalf("conflicting plugin tool was exposed: %s", pluginTool.Name())
		}
	}
}

func TestManagerInstallRejectsCrossPluginToolNameCollision(t *testing.T) {
	root := t.TempDir()
	installed := testManifest()
	installed.ID = "reports.reader"
	installedDir := filepath.Join(root, installed.ID)
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, installedDir, installed)

	source := t.TempDir()
	candidate := testManifest()
	candidate.ID = "reports_reader"
	writeManifest(t, source, candidate)
	manager := NewManager(root, "test")
	if _, err := manager.Install(source); err == nil || !strings.Contains(err.Error(), "plugin__reports_reader__read") {
		t.Fatalf("install collision error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, candidate.ID)); !os.IsNotExist(err) {
		t.Fatalf("conflicting plugin was installed: %v", err)
	}
}

func writeManifest(t *testing.T, dir string, manifest Manifest) {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func statusByID(statuses []Status, id string) Status {
	for _, status := range statuses {
		if status.ID == id {
			return status
		}
	}
	return Status{}
}
