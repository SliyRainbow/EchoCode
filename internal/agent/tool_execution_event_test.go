package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type cancellationProbeTool struct{ calls atomic.Int32 }

func (t *cancellationProbeTool) Name() string { return "cancellation_probe" }

func (*cancellationProbeTool) Description() string { return "test tool" }

func (*cancellationProbeTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }

func (t *cancellationProbeTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	t.calls.Add(1)
	return tools.Result{Summary: "should not execute"}, nil
}

func TestCancelledToolCallRecordsStartedAndStoppedTerminalWithoutExecution(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	generation, err := service.StartTaskRuntimeWithGeneration("task-tool-cancel", "2026-07-30T04:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	probe := &cancellationProbeTool{}
	registry := tools.NewRegistry(probe)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ := service.executeToolCall(ctx, registry, protocol.ToolCall{
		ID: "call-tool-cancel", Type: "function",
		Function: protocol.ToolCallFunction{Name: probe.Name(), Arguments: json.RawMessage(`{}`)},
	})
	if probe.calls.Load() != 0 {
		t.Fatalf("cancelled tool executed %d times", probe.calls.Load())
	}
	if !result.IsError || len(result.Parts) == 0 || result.Parts[0].Status != "cancelled" {
		t.Fatalf("cancelled tool result = %#v", result)
	}

	events := service.eventStore.Events()
	if len(events) != 2 || events[0].Type != eventlog.EventToolStarted || events[1].Type != eventlog.EventToolFailed {
		t.Fatalf("tool lifecycle events = %#v", events)
	}
	for _, event := range events {
		if event.ToolCallID != "call-tool-cancel" || event.RunID != "task-tool-cancel" || event.Generation != int64(generation) {
			t.Fatalf("tool event header = %#v", event.EventHeader)
		}
	}
	if len(events[1].Payload.Parts) != 1 || events[1].Payload.Parts[0].Status != "cancelled" {
		t.Fatalf("tool terminal payload = %#v", events[1].Payload)
	}
}
