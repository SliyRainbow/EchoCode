package skills

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLoaderIndexesSkillsInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "b-skill", "b-skill", "second")
	writeSkill(t, root, "a-skill", "a-skill", "first")

	index, err := NewLoader(root).Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatalf("got %d skills, want 2", len(index))
	}
	if index[0].Name != "a-skill" || index[1].Name != "b-skill" {
		t.Fatalf("skills not sorted by name: %#v", index)
	}
	if index[0].SHA256 == "" {
		t.Fatal("expected sha256 to be recorded")
	}
}

func TestFSLoaderLoadsFullSkillBody(t *testing.T) {
	content := "---\nname: embedded-skill\ndescription: embedded trigger\n---\n# Full instructions\nDo the work.\n"
	loader := NewFSLoader(fstest.MapFS{
		"skills/embedded/SKILL.md": &fstest.MapFile{Data: []byte(content)},
	}, "skills")
	index, err := loader.Index()
	if err != nil || len(index) != 1 {
		t.Fatalf("index = %#v, err = %v", index, err)
	}
	loaded, err := loader.Load("embedded-skill")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != content || loaded.SHA256 == "" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestLoaderParsesExplicitAndManualTriggers(t *testing.T) {
	loader := NewFSLoader(fstest.MapFS{
		"skills/auto/SKILL.md":               &fstest.MapFile{Data: []byte("---\nname: office-artifacts\ndescription: Office documents\n---\n# Office\n")},
		"skills/auto/agents/echocode.yaml":   &fstest.MapFile{Data: []byte("activation: auto\ntrigger: .xlsx | excel workbook\n")},
		"skills/always/SKILL.md":             &fstest.MapFile{Data: []byte("---\nname: persistent-rules\ndescription: Persistent rules\n---\n# Rules\n")},
		"skills/always/agents/echocode.yaml": &fstest.MapFile{Data: []byte("activation: always\n")},
		"skills/manual/SKILL.md":             &fstest.MapFile{Data: []byte("---\nname: manual-helper\ndescription: Manual helper\n---\n# Manual\n")},
		"skills/manual/agents/echocode.yaml": &fstest.MapFile{Data: []byte("activation: manual\n")},
	}, "skills")
	index, err := loader.Index()
	if err != nil || len(index) != 3 {
		t.Fatalf("index = %#v, err = %v", index, err)
	}
	entries := map[string]IndexEntry{}
	for _, entry := range index {
		entries[entry.Name] = entry
	}
	explicit := entries["office-artifacts"]
	if explicit.Trigger != ".xlsx | excel workbook" || explicit.TriggerMode != "explicit" {
		t.Fatalf("explicit trigger entry = %#v", explicit)
	}
	manual := entries["manual-helper"]
	if manual.TriggerMode != "manual" {
		t.Fatalf("manual trigger entry = %#v", manual)
	}
	always := entries["persistent-rules"]
	if always.Trigger != "always" || always.TriggerMode != "always" {
		t.Fatalf("persistent trigger entry = %#v", always)
	}
}

func TestDiskLoaderExposesOnlyContainedSkillPath(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "disk-skill", "disk-skill", "disk trigger")
	loader := NewLoader(root).WithOrigin("project")

	index, err := loader.Index()
	if err != nil || len(index) != 1 {
		t.Fatalf("index = %#v, err = %v", index, err)
	}
	if index[0].Source != "project" || index[0].Path != "disk-skill/SKILL.md" {
		t.Fatalf("unexpected source metadata: %#v", index[0])
	}
	loaded, err := loader.Load("disk-skill")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "disk-skill", "SKILL.md")
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		t.Fatal(err)
	}
	loadedInfo, err := os.Stat(loaded.FilePath)
	if err != nil {
		t.Fatalf("loaded file path is invalid: %v", err)
	}
	if !os.SameFile(loadedInfo, expectedInfo) {
		t.Fatalf("file path = %q, want %q", loaded.FilePath, expected)
	}
	if loaded.Source != "project" || loaded.Path != "disk-skill/SKILL.md" {
		t.Fatalf("loaded source metadata = %#v", loaded)
	}
}

func writeSkill(t *testing.T, root, dir, name, description string) {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
