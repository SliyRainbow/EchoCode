package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinManifestsAreValidAndStable(t *testing.T) {
	manifests := builtinManifests()
	if len(manifests) != 1 || manifests[0].ID != ArtifactPluginID {
		t.Fatalf("builtins = %#v", manifests)
	}
	for _, manifest := range manifests {
		if err := ValidateManifest(manifest, true); err != nil {
			t.Fatalf("validate %s: %v", manifest.ID, err)
		}
		for index := 1; index < len(manifest.Tools); index++ {
			if manifest.Tools[index-1].Name > manifest.Tools[index].Name {
				t.Fatalf("tools for %s are not sorted", manifest.ID)
			}
		}
	}
	if len(manifests[0].Tools) != 13 || manifests[0].Tools[0].Name != "document_create" {
		t.Fatalf("artifact tools = %#v", manifests[0].Tools)
	}
}

func TestValidateManifestRejectsReadOnlyWritePermission(t *testing.T) {
	manifest := testManifest()
	manifest.Permissions.FileWrite = true
	manifest.Tools[0].ReadOnly = true
	manifest.Tools[0].Permissions.FileWrite = true
	manifest.Tools[0].Paths = []PathRequirement{{Argument: "path", Access: "write"}}
	if err := ValidateManifest(manifest, false); err == nil {
		t.Fatal("expected read-only write permission to be rejected")
	}
}

func TestDecodeManifestRejectsUnknownFields(t *testing.T) {
	manifest := testManifest()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeManifest(data, false); err == nil {
		t.Fatal("expected unknown manifest field to be rejected")
	}
}

func TestValidateManifestRejectsNamespacedToolNameOverflowAndCollision(t *testing.T) {
	tooLong := testManifest()
	tooLong.ID = "plugin-with-a-name-that-is-deliberately-far-too-long-for-tools"
	if err := ValidateManifest(tooLong, false); err == nil || !strings.Contains(err.Error(), "longer than 64") {
		t.Fatalf("overflow error = %v", err)
	}

	collision := testManifest()
	collision.Tools = append(collision.Tools, collision.Tools[0])
	collision.Tools[0].Name = "read.report"
	collision.Tools[1].Name = "read_report"
	if err := ValidateManifest(collision, false); err == nil || !strings.Contains(err.Error(), "same namespaced name") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestNormalizeSettingsKeepsDeterministicEntries(t *testing.T) {
	settings := NormalizeSettings(Settings{
		Entries: []Setting{{ID: "z-plugin", Enabled: true}, {ID: "bad id"}, {ID: "a-plugin", Enabled: true}, {ID: "z-plugin"}},
	})
	if len(settings.Entries) != 3 || settings.Entries[0].ID != "a-plugin" || settings.Entries[1].ID != ArtifactPluginID || settings.Entries[2].ID != "z-plugin" {
		t.Fatalf("entries = %#v", settings.Entries)
	}
	if settings.MaxExecutionSeconds != 120 || settings.MaxOutputBytes != 1024*1024 {
		t.Fatalf("limits = %#v", settings)
	}
}

func TestNormalizeSettingsMigratesLegacyOfficeEntries(t *testing.T) {
	settings := NormalizeSettings(Settings{Entries: []Setting{{ID: legacyOfficePluginID, Enabled: true}, {ID: legacyAccessPluginID, Enabled: true}}})
	if len(settings.Entries) != 1 || settings.Entries[0].ID != ArtifactPluginID || !settings.Entries[0].Enabled || !settings.Entries[0].Permissions.FileWrite {
		t.Fatalf("migrated settings = %#v", settings.Entries)
	}
}

func testManifest() Manifest {
	return Manifest{
		SchemaVersion: 1,
		ID:            "test-plugin",
		Name:          "Test Plugin",
		Version:       "1.0.0",
		Runtime:       Runtime{Transport: "stdio", Command: "test-plugin"},
		Permissions:   PermissionSpec{FileRead: true},
		Tools: []ToolManifest{{
			Name:        "read",
			Description: "Read something",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false},
			ReadOnly:    true,
			Permissions: PermissionSpec{FileRead: true},
		}},
	}
}
