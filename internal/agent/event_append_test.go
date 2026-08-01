package agent

import (
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
)

func TestAgentEventsCarryProjectSessionRunAndGeneration(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	generation, err := service.StartTaskRuntimeWithGeneration("task-header", "2026-07-30T03:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	service.recordUserEvent("persist execution identity")

	events := service.eventStore.Events()
	if len(events) != 1 || events[0].Type != eventlog.EventUserMessage {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	if event.ProjectID == "" || event.SessionID == "" {
		t.Fatalf("event identity is incomplete: %#v", event.EventHeader)
	}
	if event.RunID != "task-header" || event.Generation != int64(generation) {
		t.Fatalf("event runtime identity = %#v", event.EventHeader)
	}
	if event.BranchID == "" || event.BranchID != service.eventStore.BranchID() {
		t.Fatalf("event branch identity = %#v, current=%q", event.EventHeader, service.eventStore.BranchID())
	}
}

func TestAgentEventToolCallIdentityIsOptionalAndExplicit(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	if _, err := service.appendEvent(eventlog.EventPayload{Content: "tool output"}, eventlog.EventArtifactUpdate, "call-7"); err != nil {
		t.Fatal(err)
	}
	events := service.eventStore.Events()
	if len(events) != 1 || events[0].ToolCallID != "call-7" {
		t.Fatalf("tool event header = %#v", events)
	}
}
