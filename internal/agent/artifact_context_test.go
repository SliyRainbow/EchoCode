package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestProtocolAssistantContentAddsAbsoluteArtifactPath(t *testing.T) {
	workspace := t.TempDir()
	service := &Service{runtimeSettings: RuntimeSettings{WorkspaceRoot: workspace}}
	relativePath := filepath.Join("reports", "attendance.xlsx")

	content := service.protocolAssistantContent("created", []tools.ResultPart{{
		Kind: tools.PartFile, Path: relativePath, FileAction: "created", Created: true,
	}})

	expected := filepath.Join(workspace, relativePath)
	if !strings.Contains(content, expected) || !strings.Contains(content, localArtifactContextStart) {
		t.Fatalf("artifact context = %q, want absolute path %q", content, expected)
	}
	if visible := stripLocalArtifactContext(content); visible != "created" {
		t.Fatalf("visible content = %q, want created", visible)
	}
}

func TestArtifactPathSurvivesSessionSwitchAndRestart(t *testing.T) {
	base := t.TempDir()
	config := ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	}
	artifactPath := filepath.Join(base, "exports", "attendance.xlsx")
	canonicalArtifactPath := canonicalArtifactTestPath(t, artifactPath)
	service := NewService(config)
	projectID, sessionID := service.ActiveSessionIDs()
	service.recordUserEvent("create workbook")
	service.sessionState.TurnCount = 1
	exitCode := 1
	service.recordAssistantAndCheckpoint("created workbook", "test-model", []tools.ResultPart{
		{Kind: tools.PartFile, Path: artifactPath, FileAction: "created", Created: true},
		{
			Kind: tools.PartToolCall, Name: "run_command", Status: "error", ToolCallID: "verify-1",
			Input: "verify workbook", Stderr: "renderer unavailable", ExitCode: &exitCode,
		},
	})

	if _, err := service.NewSession(); err != nil {
		t.Fatal(err)
	}
	if records := service.ListSessionArtifacts(); len(records) != 0 {
		t.Fatalf("new session inherited artifacts: %#v", records)
	}
	if _, err := service.SwitchProjectSession(projectID, sessionID); err != nil {
		t.Fatal(err)
	}
	if records := service.ListSessionArtifacts(); len(records) != 1 || records[0].Path != canonicalArtifactPath {
		t.Fatalf("switched session artifact registry = %#v", records)
	}
	assertArtifactModelContext(t, service.sessionMessages, canonicalArtifactPath)
	assertExecutionModelContext(t, service.sessionMessages, "renderer unavailable")
	assertArtifactHiddenFromHistory(t, service.GetSessionMessages())
	service.Close()

	restarted := NewService(config)
	defer restarted.Close()
	assertArtifactModelContext(t, restarted.sessionMessages, canonicalArtifactPath)
	assertExecutionModelContext(t, restarted.sessionMessages, "renderer unavailable")
	assertArtifactHiddenFromHistory(t, restarted.GetSessionMessages())
}

func assertArtifactModelContext(t *testing.T, messages []protocol.Message, artifactPath string) {
	t.Helper()
	for _, message := range messages {
		if strings.Contains(message.Content, artifactPath) && strings.Contains(message.Content, localArtifactContextStart) {
			return
		}
	}
	t.Fatalf("artifact path %q missing from model messages: %#v", artifactPath, messages)
}

func assertArtifactHiddenFromHistory(t *testing.T, messages []SessionMessage) {
	t.Helper()
	for _, message := range messages {
		if strings.Contains(message.Content, localArtifactContextStart) || strings.Contains(message.Content, executionContextStart) {
			t.Fatalf("private model context leaked into visible history: %#v", message)
		}
	}
}

func assertExecutionModelContext(t *testing.T, messages []protocol.Message, expected string) {
	t.Helper()
	for _, message := range messages {
		if strings.Contains(message.Content, executionContextStart) && strings.Contains(message.Content, expected) {
			return
		}
	}
	t.Fatalf("execution checkpoint %q missing from model messages: %#v", expected, messages)
}
