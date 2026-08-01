package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportSkillMarkdownInstallsDurableUserSkill(t *testing.T) {
	root := filepath.Join(t.TempDir(), "user-skills")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), UserSkillsDir: root})
	result, err := service.ImportSkillMarkdown("Release Review.md", "# Release Review\n\nCheck tests and release notes before publishing.\n")
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "release-review" || result.Skill.Name != result.Name || result.Skill.Source != "user" || result.Skill.TriggerMode != "always" {
		t.Fatalf("import result = %#v", result)
	}
	target := filepath.Join(root, result.Name, "SKILL.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, expected := range []string{"---", "name: release-review", "description:", "# Release Review"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("imported content missing %q: %q", expected, content)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(root, result.Name, "agents", "echocode.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(metadata)) != "activation: always" {
		t.Fatalf("activation metadata = %q", metadata)
	}
	found := false
	for _, entry := range result.State.SkillsIndex {
		if entry.Name == result.Name {
			found = true
		}
	}
	if !found {
		t.Fatalf("refreshed index does not contain %q: %#v", result.Name, result.State.SkillsIndex)
	}
}

func TestLegacyImportedMarkdownLoadsAsPersistentUserRules(t *testing.T) {
	root := filepath.Join(t.TempDir(), "user-skills")
	directory := filepath.Join(root, "agents")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: agents\ndescription: Collaboration workflow\n---\n\n# 协作流程\n\n每次任务开始前先列清单。\n每一步开始前说明验收标准。\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), UserSkillsDir: root})
	defer service.Close()
	preview := service.contextPreviewForInput("我们的协作流程是什么？")
	persistent := ""
	triggered := ""
	for _, section := range preview.StablePrefix {
		if section.Name == "persistent_user_rules" {
			persistent = section.Content
		}
	}
	for _, section := range preview.VolatileTail {
		if section.Name == "triggered_skills" {
			triggered = section.Content
		}
	}
	if !strings.Contains(persistent, "每次任务开始前先列清单") || !strings.Contains(persistent, "每一步开始前说明验收标准") {
		t.Fatalf("persistent rules were not loaded: %q", persistent)
	}
	if prompt := formatStablePrompt(preview); !strings.Contains(prompt, "每次任务开始前先列清单") || !strings.Contains(prompt, "summarize their actual requirements faithfully") {
		t.Fatalf("persistent rules did not reach the provider system prompt: %q", prompt)
	}
	if strings.TrimSpace(triggered) != "" {
		t.Fatalf("persistent rules were duplicated as triggered skills: %q", triggered)
	}
}

func TestImportSkillMarkdownAvoidsOverwritingDuplicateName(t *testing.T) {
	root := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: root})
	first, err := service.ImportSkillMarkdown("helper.md", "---\nname: helper\ndescription: First helper\n---\n\n# First\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ImportSkillMarkdown("helper.md", "---\nname: helper\ndescription: Second helper\n---\n\n# Second\n")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "helper" || second.Name != "helper-2" {
		t.Fatalf("duplicate names = %q, %q", first.Name, second.Name)
	}
	firstContent, err := os.ReadFile(filepath.Join(root, "helper", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstContent), "# First") || strings.Contains(string(firstContent), "# Second") {
		t.Fatalf("first Skill was overwritten: %q", firstContent)
	}
}

func TestImportSkillMarkdownRejectsUnsupportedInput(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	if _, err := service.ImportSkillMarkdown("notes.txt", "content"); err == nil {
		t.Fatal("expected unsupported extension error")
	}
	if _, err := service.ImportSkillMarkdown("empty.md", "  \n"); err == nil {
		t.Fatal("expected empty Markdown error")
	}
}
