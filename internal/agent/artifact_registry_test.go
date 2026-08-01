package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestArtifactRegistryPersistsToolMetadataAcrossRestart(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")}
	service := NewService(config)
	configureArtifactTestService(service, workspace)
	service.recordUserEvent("create the report")
	result := executeArtifactTestTool(t, service, "write_file", "call-create-report", map[string]any{
		"path": "reports/report.txt", "content": "ready\n",
	})
	if result.IsError {
		t.Fatalf("write result = %#v", result)
	}
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("report created", "test-model", result.Parts)

	records := service.ListSessionArtifacts()
	if len(records) != 1 {
		t.Fatalf("artifact records = %#v", records)
	}
	record := records[0]
	expectedPath := canonicalArtifactTestPath(t, filepath.Join(workspace, "reports", "report.txt"))
	written, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	expectedDigest := sha256.Sum256(written)
	if record.Path != expectedPath || record.DisplayPath != "reports/report.txt" {
		t.Fatalf("artifact paths = %#v, want %q", record, expectedPath)
	}
	if record.SHA256 != hex.EncodeToString(expectedDigest[:]) || record.Size != int64(len(written)) {
		t.Fatalf("artifact digest metadata = %#v", record)
	}
	if record.Action != "created" || record.Status != "available" || record.StructuralVerification != "passed" {
		t.Fatalf("artifact state = %#v", record)
	}
	if record.Tool != "write_file" || record.ToolCallID != "call-create-report" || record.MessageID == "" || record.CheckpointID == "" {
		t.Fatalf("artifact ownership = %#v", record)
	}
	if record.ProjectID != "default" || record.SessionID != "default" || record.BranchID != service.eventStore.Head() {
		t.Fatalf("artifact branch identity = %#v", record)
	}
	artifactEvents := 0
	for _, event := range service.eventStore.Events() {
		if event.Type == eventlog.EventArtifactUpdate {
			artifactEvents++
		}
	}
	if artifactEvents != 1 {
		t.Fatalf("artifact update events = %d, want one deduplicated event", artifactEvents)
	}
	service.Close()

	restarted := NewService(config)
	defer restarted.Close()
	restartedRecords := restarted.ListSessionArtifacts()
	if len(restartedRecords) != 1 || restartedRecords[0].ID != record.ID || restartedRecords[0].SHA256 != record.SHA256 {
		t.Fatalf("restarted artifact registry = %#v", restartedRecords)
	}
	assertArtifactModelContext(t, restarted.sessionMessages, expectedPath)
}

func TestExecuteToolCallFeedsCanonicalArtifactContextToModelImmediately(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "sessions")})
	defer service.Close()
	configureArtifactTestService(service, workspace)
	service.recordUserEvent("create output")
	arguments, err := json.Marshal(map[string]any{"path": "output.txt", "content": "output\n"})
	if err != nil {
		t.Fatal(err)
	}
	result, message := service.executeToolCall(context.Background(), service.buildToolRegistry(), protocol.ToolCall{
		ID: "call-immediate", Type: "function",
		Function: protocol.ToolCallFunction{Name: "write_file", Arguments: arguments},
	})
	path := canonicalArtifactTestPath(t, filepath.Join(workspace, "output.txt"))
	if result.IsError || !strings.Contains(message.Content, localArtifactContextStart) || !strings.Contains(message.Content, path) {
		t.Fatalf("tool feedback did not expose canonical artifact: result=%#v message=%q", result, message.Content)
	}
	if !strings.Contains(message.Content, `"structuralVerification":"passed"`) || !strings.Contains(message.Content, `"toolCallId":"call-immediate"`) {
		t.Fatalf("tool feedback missing verification metadata: %s", message.Content)
	}
}

func TestExecuteToolCallPreservesDeclaredArtifactAfterPartialFailure(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "sessions")})
	defer service.Close()
	configureArtifactTestService(service, workspace)
	service.recordUserEvent("create report")

	path := filepath.Join(workspace, "reports", "incomplete.txt")
	registry := tools.NewRegistry(partialArtifactFailureTool{path: path})
	result, message := service.executeToolCall(context.Background(), registry, protocol.ToolCall{
		ID: "call-partial-artifact", Type: "function",
		Function: protocol.ToolCallFunction{Name: "partial_artifact_failure", Arguments: json.RawMessage(`{}`)},
	})

	canonicalPath := canonicalArtifactTestPath(t, path)
	if !result.IsError {
		t.Fatalf("partial failure must remain an error: %#v", result)
	}
	if !strings.Contains(message.Content, localArtifactContextStart) || !strings.Contains(message.Content, canonicalPath) {
		t.Fatalf("partial failure lost artifact context: %q", message.Content)
	}
	records := service.ListSessionArtifacts()
	if len(records) != 1 || records[0].Path != canonicalPath || records[0].Action != "created" || records[0].Status != "available" {
		t.Fatalf("partial failure registry = %#v", records)
	}
}

type partialArtifactFailureTool struct {
	path string
}

func (t partialArtifactFailureTool) Name() string { return "partial_artifact_failure" }

func (t partialArtifactFailureTool) Description() string { return "test-only partial artifact failure" }

func (t partialArtifactFailureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false}
}

func (t partialArtifactFailureTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return tools.Result{}, err
	}
	if err := os.WriteFile(t.path, []byte("partial artifact\n"), 0o600); err != nil {
		return tools.Result{}, err
	}
	return tools.Result{
		Summary: "文件已写入，但后续校验失败",
		IsError: true,
		Parts: []tools.ResultPart{
			{Kind: tools.PartToolCall, Name: t.Name(), Status: "error", Output: "文件已写入，但后续校验失败"},
			{Kind: tools.PartFile, Path: t.path, FileAction: "created", Created: true},
		},
	}, nil
}

func TestArtifactRegistryFollowsRewindAndBranchSwitch(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	defer service.Close()
	configureArtifactTestService(service, workspace)

	service.recordUserEvent("create baseline")
	first := executeArtifactTestTool(t, service, "write_file", "call-first", map[string]any{
		"path": "first.txt", "content": "first\n",
	})
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("first created", "test-model", first.Parts)
	checkpoint := service.ListCheckpoints()[0].ID

	service.recordUserEvent("create second")
	second := executeArtifactTestTool(t, service, "write_file", "call-second", map[string]any{
		"path": "second.txt", "content": "second\n",
	})
	service.sessionState.TurnCount = 2
	service.recordAssistantAndCheckpoint("second created", "test-model", second.Parts)
	oldLeaf := service.eventStore.Head()
	assertArtifactPaths(t, service.ListSessionArtifacts(), filepath.Join(workspace, "first.txt"), filepath.Join(workspace, "second.txt"))

	if _, err := service.RewindToCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	assertArtifactPaths(t, service.ListSessionArtifacts(), filepath.Join(workspace, "first.txt"))
	if _, err := os.Stat(filepath.Join(workspace, "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("rewind should remove second artifact, stat err = %v", err)
	}

	service.recordUserEvent("create replacement")
	replacement := executeArtifactTestTool(t, service, "write_file", "call-replacement", map[string]any{
		"path": "replacement.txt", "content": "replacement\n",
	})
	service.sessionState.TurnCount = 2
	service.recordAssistantAndCheckpoint("replacement created", "test-model", replacement.Parts)
	newLeaf := service.eventStore.Head()
	assertArtifactPaths(t, service.ListSessionArtifacts(), filepath.Join(workspace, "first.txt"), filepath.Join(workspace, "replacement.txt"))
	for _, record := range service.ListSessionArtifacts() {
		if record.BranchID != newLeaf {
			t.Fatalf("record branch = %q, want %q", record.BranchID, newLeaf)
		}
	}

	if _, err := service.SwitchBranch(oldLeaf); err != nil {
		t.Fatal(err)
	}
	assertArtifactPaths(t, service.ListSessionArtifacts(), filepath.Join(workspace, "first.txt"), filepath.Join(workspace, "second.txt"))
	if _, err := os.Stat(filepath.Join(workspace, "replacement.txt")); !os.IsNotExist(err) {
		t.Fatalf("branch switch should remove replacement artifact, stat err = %v", err)
	}
}

func TestArtifactRegistryTracksDeletionAndRewindRestoresRecord(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	defer service.Close()
	configureArtifactTestService(service, workspace)

	service.recordUserEvent("create disposable")
	created := executeArtifactTestTool(t, service, "write_file", "call-create", map[string]any{
		"path": "disposable.txt", "content": "temporary\n",
	})
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("created", "test-model", created.Parts)
	checkpoint := service.ListCheckpoints()[0].ID

	service.recordUserEvent("delete disposable")
	deleted := executeArtifactTestTool(t, service, "delete_file", "call-delete", map[string]any{"path": "disposable.txt"})
	if deleted.IsError {
		t.Fatalf("delete result = %#v", deleted)
	}
	service.sessionState.TurnCount = 2
	service.recordAssistantAndCheckpoint("deleted", "test-model", deleted.Parts)
	records := service.ListSessionArtifacts()
	if len(records) != 1 || records[0].Action != "deleted" || records[0].Status != "deleted" || records[0].ToolCallID != "call-delete" {
		t.Fatalf("deleted artifact record = %#v", records)
	}

	if _, err := service.RewindToCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	records = service.ListSessionArtifacts()
	if len(records) != 1 || records[0].Action != "created" || records[0].Status != "available" {
		t.Fatalf("rewound artifact record = %#v", records)
	}
	if content, err := tools.ReadFileText(filepath.Join(workspace, "disposable.txt")); err != nil || content.Content != "temporary\n" {
		t.Fatalf("rewound artifact content = %q, err = %v", content.Content, err)
	}
}

func TestArtifactRegistryMarksCorruptOfficeFileInvalid(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	defer service.Close()
	configureArtifactTestService(service, workspace)
	service.recordUserEvent("create workbook")
	path := filepath.Join(workspace, "broken.xlsx")
	if err := os.WriteFile(path, []byte("not an office package"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := service.recordToolArtifacts("plugin__office-artifacts__spreadsheet_create", "call-office", tools.Result{
		Parts: []tools.ResultPart{{Kind: tools.PartFile, Path: path, FileAction: "created", Created: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].FileType != "spreadsheet" || records[0].MIMEType == "" {
		t.Fatalf("office metadata = %#v", records)
	}
	if records[0].Status != "invalid" || records[0].StructuralVerification != "failed" || records[0].VisualVerification != "pending" || records[0].FailureReason == "" {
		t.Fatalf("office verification state = %#v", records[0])
	}
}

func TestArtifactContextStructuredFormatRoundTrips(t *testing.T) {
	reference := localArtifactReference{
		Path: `C:\workspace\reports\attendance.xlsx`, Action: "created", FileType: "spreadsheet",
		MIMEType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Size: 42,
		SHA256: strings.Repeat("a", 64), Status: "available", StructuralVerification: "passed",
		VisualVerification: "pending", ToolCallID: "call-office",
	}
	content := formatLocalArtifactContext([]localArtifactReference{reference}, 0)
	parsed := parseLocalArtifactReferences(content)
	if len(parsed) != 1 || parsed[0] != reference {
		t.Fatalf("parsed artifact context = %#v\ncontent=%s", parsed, content)
	}
}

func configureArtifactTestService(service *Service, workspace string) {
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
}

func executeArtifactTestTool(t *testing.T, service *Service, name, callID string, arguments map[string]any) tools.Result {
	t.Helper()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := service.executeToolCall(context.Background(), service.buildToolRegistry(), protocol.ToolCall{
		ID: callID, Type: "function", Function: protocol.ToolCallFunction{Name: name, Arguments: encoded},
	})
	return result
}

func assertArtifactPaths(t *testing.T, records []ArtifactRecord, expected ...string) {
	t.Helper()
	actual := make([]string, 0, len(records))
	for _, record := range records {
		actual = append(actual, record.Path)
	}
	if len(actual) != len(expected) {
		t.Fatalf("artifact paths = %#v, want %#v", actual, expected)
	}
	for index := range expected {
		if artifactPathKey(actual[index]) != artifactPathKey(expected[index]) {
			t.Fatalf("artifact paths = %#v, want %#v", actual, expected)
		}
	}
}

func canonicalArtifactTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := pathutil.Canonical(path)
	if err != nil {
		t.Fatalf("canonicalize artifact test path %q: %v", path, err)
	}
	return canonical
}

func TestArtifactPathKeyTreatsWindowsShortAndLongPathsAsOneFile(t *testing.T) {
	if os.PathSeparator != '\\' {
		t.Skip("Windows path aliases only")
	}
	shortPath := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(shortPath, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	longPath, err := filepath.EvalSymlinks(shortPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(shortPath, longPath) {
		t.Skip("test environment does not expose an 8.3 path alias")
	}
	if artifactPathKey(shortPath) != artifactPathKey(longPath) {
		t.Fatalf("short and long paths have different identities: %q != %q", shortPath, longPath)
	}
}
