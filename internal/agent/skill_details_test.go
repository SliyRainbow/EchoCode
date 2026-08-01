package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/skills"
)

func TestReadSkillDetailUsesProjectOverrideAndHonorsDisabledSetting(t *testing.T) {
	globalSkills := t.TempDir()
	workspace := t.TempDir()
	writeSkill(t, globalSkills, "shared", "shared-skill", "global instructions")
	writeSkill(t, filepath.Join(workspace, "skills"), "shared", "shared-skill", "project instructions")

	service := NewService(ServiceConfig{SkillsDir: globalSkills})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.Skills.Disabled = []string{"shared-skill"}
	detail, err := service.ReadSkillDetail("shared-skill")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Source != "project" || !detail.Disabled || !detail.CanOpen {
		t.Fatalf("detail metadata = %#v", detail)
	}
	if !strings.Contains(detail.Description, "project instructions") || !strings.Contains(detail.Content, "project instructions") {
		t.Fatalf("project override not returned: %#v", detail)
	}

	index := service.loadSkillsIndex()
	if len(index) != 1 || !index[0].Disabled {
		t.Fatalf("disabled index = %#v", index)
	}
	if stable := skillsStableIndexForTest(index); strings.Contains(stable, "shared-skill") {
		t.Fatalf("disabled skill leaked into stable context: %q", stable)
	}
	if triggered := service.loadTriggeredSkills("shared-skill", index); len(triggered) != 0 {
		t.Fatalf("disabled skill triggered: %#v", triggered)
	}
}

func TestSavingDisabledSkillsPersistsAndInvalidatesProviderPrefix(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	settings := service.runtimeSettings
	settings.Skills.Disabled = []string{"z-skill", "a-skill", "a-skill", " "}

	state, err := service.SaveRuntimeSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.RuntimeSettings.Skills.Disabled; len(got) != 2 || got[0] != "a-skill" || got[1] != "z-skill" {
		t.Fatalf("normalized disabled skills = %#v", got)
	}
	if !strings.Contains(state.DeepSeekSession.ResetReason, "Skills") {
		t.Fatalf("provider prefix was not invalidated: %q", state.DeepSeekSession.ResetReason)
	}

	reloaded := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	if got := reloaded.runtimeSettings.Skills.Disabled; len(got) != 2 || got[0] != "a-skill" || got[1] != "z-skill" {
		t.Fatalf("persisted disabled skills = %#v", got)
	}
}

func TestSkillFileActionsUseValidatedDiskPath(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "open-me", "open-me", "open instructions")
	var opened, revealed string
	service := NewService(ServiceConfig{
		SkillsDir: root,
		OpenFile: func(path string) error {
			opened = path
			return nil
		},
		RevealFile: func(path string) error {
			revealed = path
			return nil
		},
	})

	if err := service.OpenSkillFile("open-me"); err != nil {
		t.Fatal(err)
	}
	if err := service.RevealSkillFile("open-me"); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "open-me", "SKILL.md")
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		t.Fatal(err)
	}
	openedInfo, err := os.Stat(opened)
	if err != nil {
		t.Fatalf("validated opened skill file missing: %v", err)
	}
	revealedInfo, err := os.Stat(revealed)
	if err != nil {
		t.Fatalf("validated revealed skill file missing: %v", err)
	}
	if !os.SameFile(openedInfo, expectedInfo) || !os.SameFile(revealedInfo, expectedInfo) {
		t.Fatalf("opened = %q, revealed = %q, want %q", opened, revealed, expected)
	}
	if _, err := service.ReadSkillDetail("..\\outside"); err == nil {
		t.Fatal("unknown path-like skill name should be rejected")
	}
}

func skillsStableIndexForTest(index []skills.IndexEntry) string {
	return skills.FormatStableIndex(index)
}
