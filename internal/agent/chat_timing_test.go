package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestChatTimingTrackerMergesEachPhaseByStableToolCallID(t *testing.T) {
	startedAt := time.Now()
	var events []ChatStreamEvent
	tracker := newChatTimingTracker(startedAt, func(event ChatStreamEvent) {
		events = append(events, event)
	})
	tracker.Start("context", "正在组装项目上下文", "test-model")
	time.Sleep(2 * time.Millisecond)
	tracker.Start("model", "正在等待模型首个响应", "test-model")
	time.Sleep(2 * time.Millisecond)
	tracker.Finish("completed")

	if len(events) != 4 {
		t.Fatalf("timing events = %#v", events)
	}
	if events[0].ToolCallID != "timing:context" || events[1].ToolCallID != events[0].ToolCallID {
		t.Fatalf("context phase IDs = %#v", events[:2])
	}
	if events[0].Status != "running" || events[1].Status != "completed" || events[1].StageDurationMs < 1 {
		t.Fatalf("context phase events = %#v", events[:2])
	}
	if events[2].ToolCallID != "timing:model" || events[3].ToolCallID != events[2].ToolCallID {
		t.Fatalf("model phase IDs = %#v", events[2:])
	}
	if events[3].StageDurationMs < 1 || events[3].ElapsedMs < events[1].ElapsedMs {
		t.Fatalf("model timing = %#v", events[3])
	}
}

func TestProviderTimingEmitsConnectionFirstEventAndFirstText(t *testing.T) {
	events := make(chan protocol.StreamEvent, 3)
	events <- protocol.StreamEvent{Type: "reasoning", Delta: "thinking"}
	events <- protocol.StreamEvent{Type: "delta", Delta: "answer"}
	events <- protocol.StreamEvent{Type: "done"}
	close(events)
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}

	ctx := context.WithValue(context.Background(), chatTurnStartedAtKey{}, time.Now())
	var timingEvents []ChatStreamEvent
	result, err := collectProviderStreamWithTiming(
		ctx,
		provider,
		protocol.ChatRequest{Model: "timed-model"},
		func(event ChatStreamEvent) {
			if event.Phase != "" {
				timingEvents = append(timingEvents, event)
			}
		},
		providerStreamTiming{
			OpenTimeout:       time.Second,
			IdleTimeout:       time.Second,
			HeartbeatInterval: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "answer" || result.Reasoning != "thinking" {
		t.Fatalf("completion = %#v", result)
	}

	want := []string{"provider_connected", "provider_first_event", "provider_first_text"}
	if len(timingEvents) != len(want) {
		t.Fatalf("provider timing events = %#v", timingEvents)
	}
	for index, phase := range want {
		if timingEvents[index].Phase != phase || timingEvents[index].Status != "completed" || timingEvents[index].StageDurationMs < 1 {
			t.Fatalf("timing event %d = %#v", index, timingEvents[index])
		}
	}
}

func TestTurnTimelineUpdatesPhaseDurationInsteadOfDuplicatingCard(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "status", Phase: "context", ToolCallID: "timing:context",
		Message: "正在组装项目上下文", Status: "running",
	})
	service.captureTurnTimelineEvent(ChatStreamEvent{
		Type: "status", Phase: "context", ToolCallID: "timing:context",
		Message: "正在组装项目上下文", Status: "completed", StageDurationMs: 42,
	})
	if len(service.turnTimelineParts) != 1 {
		t.Fatalf("timeline parts = %#v", service.turnTimelineParts)
	}
	part := service.turnTimelineParts[0]
	if part.Status != "completed" || part.DurationMs != 42 || part.ToolCallID != "timing:context" {
		t.Fatalf("timing timeline part = %#v", part)
	}
}

func TestFinishedTimingPhaseIsPersistedBeforeAssistantMessage(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.resetTurnTimeline()
	ctx := withChatTiming(context.Background(), newChatTimingTracker(time.Now(), service.captureTurnTimeline(nil)))
	startChatTiming(ctx, "execution", "正在执行 Agent 任务", "test-model")
	time.Sleep(2 * time.Millisecond)
	finishChatTiming(ctx, "completed")
	service.recordAssistantAndCheckpoint("已完成", "test-model", nil, 5)

	history := service.GetSessionMessages()
	if len(history) != 1 {
		t.Fatalf("history = %#v", history)
	}
	var found bool
	for _, part := range history[0].Parts {
		if part.Kind == tools.PartTimelineNote && part.ToolCallID == "timing:execution" {
			found = true
			if part.Status != "completed" || part.DurationMs < 1 {
				t.Fatalf("persisted timing part = %#v", part)
			}
		}
	}
	if !found {
		t.Fatalf("persisted timing phase missing: %#v", history[0].Parts)
	}
}
