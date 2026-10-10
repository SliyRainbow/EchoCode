package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

func TestOfficePluginFilesRewindAndReplayAcrossRestart(t *testing.T) {
	root := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(root, "sessions"), SettingsPath: filepath.Join(root, "settings.json")}
	service := NewService(config)
	defer func() { service.Close() }()
	settings := service.WorkbenchState().RuntimeSettings
	settings.WorkspaceRoot = root
	settings.ApprovalPolicy = "never"
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	checkpoint := func(label string) string {
		t.Helper()
		if err := service.recordUserEvent(label); err != nil {
			t.Fatal(err)
		}
		service.sessionState.TurnCount++
		if err := service.recordAssistantAndCheckpoint(label, "fixture", nil); err != nil {
			t.Fatal(err)
		}
		return service.eventStore.Head()
	}
	baseline := checkpoint("baseline")
	run := func(name string, arguments string) {
		t.Helper()
		result, _ := service.executeToolCall(context.Background(), service.buildToolRegistry(), protocol.ToolCall{ID: name, Type: "function", Function: protocol.ToolCallFunction{Name: "plugin__office-artifacts__" + name, Arguments: json.RawMessage(arguments)}})
		if result.IsError {
			t.Fatal(result.Summary)
		}
	}
	run("document_create", `{"path":"report.docx","text":"before"}`)
	first := checkpoint("created")
	path := filepath.Join(root, "report.docx")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run("document_replace_text", `{"path":"report.docx","find":"before","replace":"after"}`)
	second := checkpoint("modified")
	after, err := os.ReadFile(path)
	if err != nil || bytes.Equal(before, after) {
		t.Fatal("fixture did not change")
	}
	service.Close()
	service = NewService(config)
	if _, err := service.RewindToCheckpoint(first); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, before) {
		t.Fatal("plugin rewind did not restore the exact DOCX bytes")
	}
	if _, err := service.RewindToCheckpoint(baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rewind left the plugin-created file behind")
	}
	if _, err := service.SwitchBranch(second); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, after) {
		t.Fatal("plugin branch replay did not restore the exact DOCX bytes")
	}
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RewindToCheckpoint(first); err == nil {
		t.Fatal("rewind overwrote external changes to a plugin file")
	}
}
