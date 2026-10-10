package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/plugins"
)

func TestPluginLifecycleRollsBackSettingsFailures(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeAgentTestPlugin(t, source)
	settingsPath := filepath.Join(root, "settings.json")
	installed := filepath.Join(root, "installed")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), PluginsDir: installed, SettingsPath: settingsPath})
	defer service.Close()
	before := service.WorkbenchState().RuntimeSettings
	if err := os.Mkdir(settingsPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPlugin(filepath.Join(source, "agent-integration")); err == nil {
		t.Fatal("installation should fail when settings cannot be saved")
	}
	if !reflect.DeepEqual(service.WorkbenchState().RuntimeSettings, before) {
		t.Fatal("failed install changed runtime settings")
	}
	if _, err := os.Stat(filepath.Join(installed, "agent-integration")); !os.IsNotExist(err) {
		t.Fatal("failed install left an activated package")
	}
	if err := os.Remove(settingsPath); err != nil {
		t.Fatal(err)
	}
	state, err := service.InstallPlugin(filepath.Join(source, "agent-integration"))
	if err != nil {
		t.Fatal(err)
	}
	entry, exists := plugins.SettingFor(state.RuntimeSettings.Plugins, "agent-integration")
	if !exists || entry.Enabled {
		t.Fatal("installed plugin must await explicit activation and grants")
	}
	entry.Enabled = true
	entry.Permissions = plugins.PermissionGrant{FileRead: true, FileWrite: true}
	state.RuntimeSettings.Plugins = plugins.UpsertSetting(state.RuntimeSettings.Plugins, entry)
	if _, err := service.SaveRuntimeSettings(state.RuntimeSettings); err != nil {
		t.Fatal(err)
	}
	assertRegistryContains(t, service.buildToolRegistry(), "plugin__agent-integration__inspect", "plugin__agent-integration__write")
	before = service.WorkbenchState().RuntimeSettings
	blocked := filepath.Join(root, "blocked-settings")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	service.settingsPath = blocked
	if _, err := service.UninstallPlugin("agent-integration"); err == nil {
		t.Fatal("uninstall should fail when settings cannot be saved")
	}
	if !reflect.DeepEqual(service.WorkbenchState().RuntimeSettings, before) {
		t.Fatal("failed uninstall changed runtime grants")
	}
	if _, err := os.Stat(filepath.Join(installed, "agent-integration", plugins.ManifestFileName)); err != nil {
		t.Fatalf("failed uninstall lost the package: %v", err)
	}
	assertRegistryContains(t, service.buildToolRegistry(), "plugin__agent-integration__inspect")
	service.settingsPath = settingsPath
	if _, err := service.UninstallPlugin("agent-integration"); err != nil {
		t.Fatal(err)
	}
	if _, exists := plugins.SettingFor(service.WorkbenchState().RuntimeSettings.Plugins, "agent-integration"); exists {
		t.Fatal("successful uninstall retained plugin grants")
	}
	if _, err := os.Stat(filepath.Join(installed, "agent-integration")); !os.IsNotExist(err) {
		t.Fatal("successful uninstall retained plugin files")
	}
	if entries, err := os.ReadDir(installed); err != nil || len(entries) != 0 {
		t.Fatal("plugin lifecycle left staging files")
	}
}
