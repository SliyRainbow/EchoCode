package agent

import (
	"context"
	"strings"
	"sync"
	"time"
)

type chatTimingContextKey struct{}

type chatTimingTracker struct {
	mu            sync.Mutex
	sink          ChatEventSink
	turnStartedAt time.Time
	stage         string
	message       string
	model         string
	stageStarted  time.Time
}

func newChatTimingTracker(turnStartedAt time.Time, sink ChatEventSink) *chatTimingTracker {
	return &chatTimingTracker{turnStartedAt: turnStartedAt, sink: sink}
}

func withChatTiming(ctx context.Context, tracker *chatTimingTracker) context.Context {
	if tracker == nil {
		return ctx
	}
	return context.WithValue(ctx, chatTimingContextKey{}, tracker)
}

func chatTimingFromContext(ctx context.Context) *chatTimingTracker {
	if ctx == nil {
		return nil
	}
	tracker, _ := ctx.Value(chatTimingContextKey{}).(*chatTimingTracker)
	return tracker
}

func startChatTiming(ctx context.Context, phase, message, model string) {
	if tracker := chatTimingFromContext(ctx); tracker != nil {
		tracker.Start(phase, message, model)
	}
}

// finishChatTiming closes the current phase before a result is persisted. The
// outer defer still calls Finish as a fallback, but durable history must see
// the final timing event before recordAssistantAndCheckpoint/recordTurnTerminal
// merges the turn timeline.
func finishChatTiming(ctx context.Context, status string) {
	if tracker := chatTimingFromContext(ctx); tracker != nil {
		tracker.Finish(status)
	}
}

func (t *chatTimingTracker) Start(phase, message, model string) {
	if t == nil || t.sink == nil {
		return
	}
	phase = strings.TrimSpace(phase)
	message = strings.TrimSpace(message)
	if phase == "" || message == "" {
		return
	}

	now := time.Now()
	var events []ChatStreamEvent
	t.mu.Lock()
	if t.stage == phase && t.message == message {
		t.mu.Unlock()
		return
	}
	if t.stage != "" && !t.stageStarted.IsZero() {
		events = append(events, t.eventLocked("completed", t.stage, t.message, t.model, now.Sub(t.stageStarted)))
	}
	t.stage = phase
	t.message = message
	if strings.TrimSpace(model) != "" {
		t.model = strings.TrimSpace(model)
	}
	t.stageStarted = now
	events = append(events, t.eventLocked("running", phase, message, t.model, 0))
	t.mu.Unlock()

	for _, event := range events {
		emitChatEvent(t.sink, event)
	}
}

func (t *chatTimingTracker) Finish(status string) {
	if t == nil || t.sink == nil {
		return
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "completed"
	}
	now := time.Now()
	t.mu.Lock()
	if t.stage == "" || t.stageStarted.IsZero() {
		t.mu.Unlock()
		return
	}
	event := t.eventLocked(status, t.stage, t.message, t.model, now.Sub(t.stageStarted))
	t.stage = ""
	t.message = ""
	t.stageStarted = time.Time{}
	t.mu.Unlock()
	emitChatEvent(t.sink, event)
}

func (t *chatTimingTracker) eventLocked(status, phase, message, model string, stageDuration time.Duration) ChatStreamEvent {
	elapsed := time.Since(t.turnStartedAt).Milliseconds()
	if elapsed < 1 {
		elapsed = 1
	}
	stageDurationMs := int64(0)
	if stageDuration > 0 {
		stageDurationMs = stageDuration.Milliseconds()
		if stageDurationMs < 1 {
			stageDurationMs = 1
		}
	}
	return ChatStreamEvent{
		Type:            "status",
		Phase:           phase,
		Message:         message,
		Model:           model,
		ToolCallID:      "timing:" + phase,
		Status:          status,
		ElapsedMs:       elapsed,
		StageDurationMs: stageDurationMs,
	}
}

func emitProviderTimingEvent(ctx context.Context, sink ChatEventSink, model, phase, message string, startedAt time.Time, status string) {
	if sink == nil {
		return
	}
	if status == "" {
		status = "running"
	}
	stageDuration := time.Since(startedAt)
	stageDurationMs := stageDuration.Milliseconds()
	if stageDurationMs < 1 {
		stageDurationMs = 1
	}
	elapsedMs := stageDurationMs
	if turnStartedAt, ok := ctx.Value(chatTurnStartedAtKey{}).(time.Time); ok && !turnStartedAt.IsZero() {
		elapsedMs = time.Since(turnStartedAt).Milliseconds()
		if elapsedMs < 1 {
			elapsedMs = 1
		}
	}
	emitChatEvent(sink, ChatStreamEvent{
		Type:            "status",
		Phase:           phase,
		Message:         message,
		Model:           model,
		ToolCallID:      "timing:" + phase,
		Status:          status,
		ElapsedMs:       elapsedMs,
		StageDurationMs: stageDurationMs,
	})
}
