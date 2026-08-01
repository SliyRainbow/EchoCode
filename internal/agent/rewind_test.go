package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const legacySearchFailureMessage = "网络搜索已完成，但上游模型在整理结果时连接失败。搜索来源已经保留，请展开搜索记录查看；也可以直接重试本条消息。"

func TestSessionMessagesRestoreTimingAndToolMetadata(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	exitCode := 0
	svc.recordAssistantAndCheckpoint("done", "test-model", []tools.ResultPart{{
		Kind: tools.PartToolCall, Name: "run_command", Status: "ok", Input: "go test ./...", Output: "ok",
		WorkingDirectory: `C:\workspace`, ExitCode: &exitCode,
		StartedAt: "2026-07-22T10:00:00Z", CompletedAt: "2026-07-22T10:00:02Z", DurationMs: 2_000,
	}}, 12_450)

	history := svc.GetSessionMessages()
	if len(history) != 1 || history[0].DurationMs != 12_450 || len(history[0].Parts) != 1 {
		t.Fatalf("restored message metadata = %#v", history)
	}
	part := history[0].Parts[0]
	if part.WorkingDirectory != `C:\workspace` || part.ExitCode == nil || *part.ExitCode != 0 || part.DurationMs != 2_000 {
		t.Fatalf("restored tool metadata = %#v", part)
	}
}

func TestProviderNoticePersistsAcrossServiceRestart(t *testing.T) {
	sessionsDir := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessionsDir}
	retryable := false
	service := NewService(config)
	service.recordAssistantAndCheckpoint("request blocked", "gpt-requested", []tools.ResultPart{{
		Kind: tools.PartProviderNotice, NoticeKind: "policy_error", Severity: "error",
		Message: "request blocked", RequestID: "req-policy", ErrorCode: "cyber_policy",
		HTTPStatus: 400, Retryable: &retryable,
	}})
	service.Close()

	restored := NewService(config)
	defer restored.Close()
	history := restored.GetSessionMessages()
	if len(history) != 1 || len(history[0].Parts) != 1 {
		t.Fatalf("restored history = %#v", history)
	}
	part := history[0].Parts[0]
	if part.Kind != tools.PartProviderNotice || part.NoticeKind != "policy_error" || part.ErrorCode != "cyber_policy" || part.RequestID != "req-policy" || part.Retryable == nil || *part.Retryable {
		t.Fatalf("restored provider notice = %#v", part)
	}
}

func TestGetSessionMessagesUpgradesLegacyWebSearchFailure(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	_, err := svc.eventStore.Append(eventlog.EventPayload{
		Role:    "assistant",
		Content: legacySearchFailureMessage,
		Model:   "test-model",
		Parts: []eventlog.MessagePart{
			{Kind: string(tools.PartToolCall), Name: "web_search", Status: "ok"},
			{
				Kind:  string(tools.PartWebSearch),
				Query: "宁波台风预警",
				Sources: []eventlog.MessageSearchSource{{
					Title: "宁波天气预警", URL: "https://weather.example/warning", Snippet: "宁波市气象台发布最新预警。",
				}},
			},
			{Kind: string(tools.PartText), Text: legacySearchFailureMessage},
		},
	}, eventlog.EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}

	history := svc.GetSessionMessages()
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	message := history[0]
	for _, expected := range []string{"本轮未完成最终分析", "原始网络搜索记录", "宁波天气预警", "https://weather.example/warning"} {
		if !strings.Contains(message.Content, expected) {
			t.Fatalf("restored content missing %q: %s", expected, message.Content)
		}
	}
	if strings.Contains(message.Content, "整理结果时连接失败") {
		t.Fatalf("legacy placeholder was not upgraded: %s", message.Content)
	}
	if len(message.Parts) != 3 || message.Parts[2].Kind != tools.PartText || message.Parts[2].Text != message.Content {
		t.Fatalf("restored parts do not contain upgraded text: %+v", message.Parts)
	}
}

func TestDeletedFileRewindsAndReplaysAcrossBranch(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "delete-me.txt")
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	meta := tools.FileText{LineEnding: tools.LineEndingCRLF, Encoding: tools.EncodingUTF16LE, HadBOM: true}
	if err := tools.WriteFileTextAtomic(target, "需要恢复\n", meta); err != nil {
		t.Fatal(err)
	}

	svc.recordUserEvent("baseline")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("baseline ready", "m", nil)
	baseline := svc.ListCheckpoints()[0].ID

	svc.recordUserEvent("delete it")
	if err := svc.recordFileSnapshot(tools.FileChange{
		Path: "delete-me.txt", Before: "需要恢复\n", Existed: true, Deleted: true,
		LineEnding: string(meta.LineEnding), Encoding: string(meta.Encoding), HadBOM: meta.HadBOM,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("deleted", "m", nil)
	deletedLeaf := svc.eventStore.Head()

	if _, err := svc.RewindToCheckpoint(baseline); err != nil {
		t.Fatal(err)
	}
	restored, err := tools.ReadFileText(target)
	if err != nil || restored.Content != "需要恢复\n" || restored.Encoding != tools.EncodingUTF16LE || !restored.HadBOM {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}

	if _, err := svc.SwitchBranch(deletedLeaf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted branch should remove file, stat err=%v", err)
	}
}

func TestGetSessionMessagesKeepsLegacySearchFailureWithoutSources(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	_, err := svc.eventStore.Append(eventlog.EventPayload{
		Role:    "assistant",
		Content: legacySearchFailureMessage,
		Parts:   []eventlog.MessagePart{{Kind: string(tools.PartText), Text: legacySearchFailureMessage}},
	}, eventlog.EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}

	history := svc.GetSessionMessages()
	if len(history) != 1 || history[0].Content != legacySearchFailureMessage || history[0].Parts[0].Text != legacySearchFailureMessage {
		t.Fatalf("message without durable sources must stay unchanged: %+v", history)
	}
}

func TestNewServiceRestoresMigratedConversationContext(t *testing.T) {
	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions}
	svc := NewService(config)
	svc.recordUserEvent("宁波今天有台风预警吗？")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint(legacySearchFailureMessage, "test-model", []tools.ResultPart{
		{
			Kind:  tools.PartWebSearch,
			Query: "宁波台风预警",
			Sources: []tools.SearchSource{{
				Title: "宁波天气预警", URL: "https://weather.example/warning", Snippet: "宁波市气象台发布最新预警。",
			}},
		},
		{Kind: tools.PartText, Text: legacySearchFailureMessage},
	})

	reloaded := NewService(config)
	if len(reloaded.sessionMessages) != 2 {
		t.Fatalf("restored session message count = %d, want 2", len(reloaded.sessionMessages))
	}
	if reloaded.sessionMessages[0].Role != "user" || reloaded.sessionMessages[0].Content != "宁波今天有台风预警吗？" {
		t.Fatalf("restored user message is incorrect: %+v", reloaded.sessionMessages[0])
	}
	restored := reloaded.sessionMessages[1]
	if restored.Role != "assistant" || !strings.Contains(restored.Content, "本轮未完成最终分析") || !strings.Contains(restored.Content, "宁波天气预警") || strings.Contains(restored.Content, "整理结果时连接失败") {
		t.Fatalf("restored assistant context was not migrated: %+v", restored)
	}
	if reloaded.sessionState.TurnCount != 1 {
		t.Fatalf("restored turn count = %d, want 1", reloaded.sessionState.TurnCount)
	}
}

// TestRewindRestoresFileAndConversation 验证 Rewind 同时回退文件内容与对话。
func TestRewindRestoresFileAndConversation(t *testing.T) {
	workspace := t.TempDir()
	sessions := t.TempDir()
	target := filepath.Join(workspace, "note.txt")

	svc := NewService(ServiceConfig{
		SkillsDir:   t.TempDir(),
		SessionsDir: sessions,
	})
	if svc.eventStore == nil {
		t.Fatal("事件存储应已初始化")
	}
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"

	// 写入初始文件内容（模拟改动前状态 v1）。
	if err := tools.WriteFileTextAtomic(target, "v1\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}

	// 模拟第 1 轮：用户消息 + 文件从 v1 改到 v2 + assistant + checkpoint。
	svc.sessionMessages = nil
	svc.recordUserEvent("把文件改成 v2")
	svc.recordFileSnapshot(tools.FileChange{
		Path: "note.txt", Before: "v1\n", After: "v2\n", Existed: true, LineEnding: "lf",
	})
	if err := tools.WriteFileTextAtomic(target, "v2\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("已改为 v2", "test-model", nil)

	checkpoints := svc.ListCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("检查点数 = %d, want 1", len(checkpoints))
	}
	cp1 := checkpoints[0].ID

	// 第 2 轮：文件从 v2 改到 v3。
	svc.recordUserEvent("再改成 v3")
	svc.recordFileSnapshot(tools.FileChange{
		Path: "note.txt", Before: "v2\n", After: "v3\n", Existed: true, LineEnding: "lf",
	})
	if err := tools.WriteFileTextAtomic(target, "v3\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("已改为 v3", "test-model", nil)

	// 磁盘现在应是 v3。
	if got, _ := tools.ReadFileText(target); got.Content != "v3\n" {
		t.Fatalf("回退前文件内容 = %q, want v3", got.Content)
	}

	// Rewind 到第 1 轮的 checkpoint：文件应回到 v2（第 1 轮结束时的状态）。
	if _, err := svc.RewindToCheckpoint(cp1); err != nil {
		t.Fatal(err)
	}
	if got, _ := tools.ReadFileText(target); got.Content != "v2\n" {
		t.Fatalf("回退后文件内容 = %q, want v2", got.Content)
	}

	// 对话线也应回到第 1 轮（head 在 cp1）。
	if svc.eventStore.Head() != cp1 {
		t.Fatalf("head = %q, want %q", svc.eventStore.Head(), cp1)
	}
	// 当前对话线只应含第 1 轮的事件（不含第 2 轮）。
	for _, ev := range svc.eventStore.Events() {
		if ev.Type == eventlog.EventUserMessage && ev.Payload.Content == "再改成 v3" {
			t.Fatal("回退后当前线不应包含第 2 轮的用户消息")
		}
	}
}
