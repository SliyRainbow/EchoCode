package agent

import (
	"fmt"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestTurnTimelinePersistsProgressAroundToolActivity(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.resetTurnTimeline()
	sink := service.captureTurnTimeline(nil)
	sink(ChatStreamEvent{Type: "status", Message: "正在分析任务", Status: "running"})
	sink(ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "call-1", ToolInput: "go test ./...", Status: "running",
	})
	sink(ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "call-1", ToolInput: "go test ./...", Status: "completed",
		Message: "测试通过", Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: "run_command", ToolCallID: "call-1", Status: "ok",
			Input: "go test ./...", Output: "ok",
		}},
	})
	sink(ChatStreamEvent{Type: "status", Message: "正在整理验证结果", Status: "running"})
	service.recordAssistantAndCheckpoint("全部验证通过。", "test-model", nil, 1_250)

	history := service.GetSessionMessages()
	if len(history) != 1 {
		t.Fatalf("history = %#v", history)
	}
	parts := history[0].Parts
	if len(parts) != 3 {
		t.Fatalf("timeline parts = %#v", parts)
	}
	if parts[0].Kind != tools.PartToolCall || parts[0].Status != "ok" || parts[0].Output != "ok" {
		t.Fatalf("tool activity = %#v", parts[0])
	}
	if parts[1].Kind != tools.PartTimelineNote || parts[1].Message != "正在整理验证结果" {
		t.Fatalf("progress note = %#v", parts[1])
	}
	if parts[2].Kind != tools.PartText || parts[2].Text != "全部验证通过。" {
		t.Fatalf("final answer = %#v", parts[2])
	}
}

func TestTurnTimelineSettlesProgressOnFailure(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.resetTurnTimeline()
	service.captureTurnTimelineEvent(ChatStreamEvent{Type: "status", Message: "连接中断，正在重试", Status: "retrying"})
	if err := service.recordTurnTerminal("failed", "模型服务仍不可用", "test-model", nil, 500); err != nil {
		t.Fatal(err)
	}
	history := service.GetSessionMessages()
	if len(history) != 1 || history[0].Status != "failed" || len(history[0].Parts) != 2 {
		t.Fatalf("failed timeline = %#v", history)
	}
	if history[0].Parts[0].Kind != tools.PartTimelineNote || history[0].Parts[0].Status != "failed" {
		t.Fatalf("failed progress note = %#v", history[0].Parts[0])
	}
}

func TestTurnTimelineSettlesPreviousProgressWhenWorkAdvances(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "status", ToolCallID: "progress-1", Message: "已连接服务器。", Status: "running",
	})
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "status", ToolCallID: "progress-2", Message: "正在读取配置。", Status: "running",
	})
	if len(service.turnTimelineParts) != 2 || service.turnTimelineParts[0].Status != "completed" || service.turnTimelineParts[1].Status != "running" {
		t.Fatalf("progress statuses = %#v", service.turnTimelineParts)
	}
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "tool", ToolName: "ssh", ToolCallID: "ssh-read", Status: "running",
	})
	if service.turnTimelineParts[1].Status != "completed" {
		t.Fatalf("tool start did not settle current milestone: %#v", service.turnTimelineParts)
	}
}

func TestTurnTimelineDoesNotPersistProviderHeartbeats(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.resetTurnTimeline()
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "heartbeat", Message: "upstream still processing", Status: "waiting",
	})
	if len(service.turnTimelineParts) != 0 {
		t.Fatalf("provider heartbeat leaked into durable timeline: %#v", service.turnTimelineParts)
	}
}

func TestTurnTimelineDoesNotPersistRoutineTaskStatuses(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	for _, message := range []string{"正在准备上下文", "正在连接 Anthropic", "正在分析任务", "正在生成执行计划"} {
		service.captureTurnTimelineEvent(ChatStreamEvent{Type: "status", Message: message, Status: "running"})
	}
	if len(service.turnTimelineParts) != 0 {
		t.Fatalf("routine status leaked into durable timeline: %#v", service.turnTimelineParts)
	}
}

func TestTurnTimelineMarksOverflowInsteadOfSilentlyDroppingNotes(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	for index := 0; index < maxTurnTimelineNotes+10; index++ {
		service.captureTurnTimelineEvent(ChatStreamEvent{
			Type: "status", Message: fmt.Sprintf("正在核验步骤 %d", index), Status: "running",
		})
	}
	if notes := timelineNoteCount(service.turnTimelineParts); notes != maxTurnTimelineNotes {
		t.Fatalf("timeline note count = %d, want %d", notes, maxTurnTimelineNotes)
	}
	last := service.turnTimelineParts[len(service.turnTimelineParts)-1]
	if last.Kind != tools.PartTimelineNote || last.Message != timelineOverflowMessage {
		t.Fatalf("timeline overflow marker = %#v", last)
	}
}

func TestTurnTimelineDropsEventsFromSupersededGeneration(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	oldGeneration, err := service.StartTaskRuntimeWithGeneration("task-timeline", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	forwarded := 0
	oldSink := service.captureTurnTimelineForGeneration(oldGeneration, func(ChatStreamEvent) {
		forwarded++
	})

	currentGeneration, err := service.StartTaskRuntimeWithGeneration("task-timeline", "2026-07-30T01:00:01Z")
	if err != nil {
		t.Fatal(err)
	}
	service.resetTurnTimeline()
	currentSink := service.captureTurnTimelineForGeneration(currentGeneration, func(ChatStreamEvent) {
		forwarded++
	})

	oldSink(ChatStreamEvent{Type: "status", Message: "旧任务仍在输出", Status: "running"})
	if forwarded != 0 || len(service.turnTimelineParts) != 0 {
		t.Fatalf("superseded timeline event leaked: forwarded=%d parts=%#v", forwarded, service.turnTimelineParts)
	}
	currentSink(ChatStreamEvent{Type: "status", Message: "新任务正在工作", Status: "running"})
	if forwarded != 1 || len(service.turnTimelineParts) != 1 || service.turnTimelineParts[0].Message != "新任务正在工作" {
		t.Fatalf("current timeline event missing: forwarded=%d parts=%#v", forwarded, service.turnTimelineParts)
	}
}
