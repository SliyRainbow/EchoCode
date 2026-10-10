package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFailedSettingsSaveKeepsCurrentRuntimePolicy(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "runtime-settings.json")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: blocked})
	defer service.Close()
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	before := service.WorkbenchState().RuntimeSettings
	next := before.Normalized()
	next.WorkspaceRoot = t.TempDir()
	next.NetworkAccess = !before.NetworkAccess
	next.Team.Enabled = !before.Team.Enabled
	if _, err := service.SaveRuntimeSettings(next); err == nil {
		t.Fatal("expected persistence failure")
	}
	after := service.WorkbenchState().RuntimeSettings
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed save changed the live runtime policy")
	}
}
