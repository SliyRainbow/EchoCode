package eventlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExecutionEventPayloadRoundTrips(t *testing.T) {
	exitCode := 0
	payloads := []ExecutionEventPayload{
		&UserMessagePayload{MessageID: "message-user", Content: "build it"},
		&AssistantDeltaPayload{MessageID: "message-assistant", Delta: "working", Index: 2},
		&AssistantCompletedPayload{
			MessageID: "message-assistant", Content: "done", Model: "gpt-test", FinishReason: "stop",
			DurationMs: 15, Usage: &ExecutionTokenUsage{InputTokens: 11, OutputTokens: 5, CachedInputTokens: 7},
		},
		&ToolStartedPayload{Name: "write_file", Input: json.RawMessage(`{"path":"result.txt"}`), WorkingDirectory: `C:\work`},
		&ToolOutputPayload{Name: "run_command", Channel: ToolOutputStdout, Content: "ok\n", Sequence: 1},
		&ToolCompletedPayload{Name: "run_command", Result: json.RawMessage(`{"status":"ok"}`), ExitCode: &exitCode, DurationMs: 20},
		&ToolFailedPayload{Name: "run_command", Error: "exit status 1", ErrorCode: "exit_nonzero", Retryable: true, ExitCode: &exitCode, DurationMs: 21},
		&TurnInterruptedPayload{Reason: "user_cancelled", LastEventID: "ev-7", HadAssistantOutput: true},
		&ContextCondensedPayload{
			Summary: "kept the active plan", SourceEventIDs: []string{"ev-1", "ev-7"},
			PreservedToolCallIDs: []string{"call-1"}, PreservedArtifactIDs: []string{"artifact-1"},
			InputTokenCount: 4000, OutputTokenCount: 600,
		},
		&TaskTerminalPayload{Status: TaskTerminalCompleted, Summary: "verified", CheckpointID: "cp-1", DurationMs: 42},
	}

	wantKinds := []ExecutionEventKind{
		ExecutionKindUserMessage,
		ExecutionKindAssistantDelta,
		ExecutionKindAssistantCompleted,
		ExecutionKindToolStarted,
		ExecutionKindToolOutput,
		ExecutionKindToolCompleted,
		ExecutionKindToolFailed,
		ExecutionKindTurnInterrupted,
		ExecutionKindContextCondensed,
		ExecutionKindTaskTerminal,
	}

	for index, wantPayload := range payloads {
		wantKind := wantKinds[index]
		t.Run(string(wantKind), func(t *testing.T) {
			header := EventHeader{
				ProjectID: "project-1", SessionID: "session-1", BranchID: "branch-1",
				RunID: "run-1", Generation: 3, CausationID: "ev-parent",
			}
			if requiresToolCallID(wantKind) {
				header.ToolCallID = "call-1"
			}
			wantEvent, err := NewExecutionEventEnvelope(
				header, "ev-8", "ev-parent", 8, time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC), wantPayload,
			)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(wantEvent)
			if err != nil {
				t.Fatal(err)
			}
			gotEvent, gotPayload, err := DecodeExecutionEvent(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if gotEvent.Kind != wantKind || gotEvent.EventHeader != wantEvent.EventHeader || gotEvent.ID != wantEvent.ID || gotEvent.Seq != wantEvent.Seq {
				t.Fatalf("event round trip mismatch: got %#v, want %#v", gotEvent, wantEvent)
			}
			if !reflect.DeepEqual(gotPayload, wantPayload) {
				t.Fatalf("payload round trip mismatch:\n got  %#v\n want %#v", gotPayload, wantPayload)
			}
		})
	}
}

func TestExecutionEventDecodesLegacyPayloadWithoutChangingEventPayload(t *testing.T) {
	legacyJSON := []byte(`{"id":"ev-legacy","parentId":"","seq":7,"type":"user_message","ts":"2026-07-30T00:00:00Z","payload":{"role":"user","content":"legacy message","model":"legacy-model"}}`)

	var legacy Event
	if err := json.Unmarshal(legacyJSON, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Type != EventUserMessage || legacy.Payload.Role != "user" || legacy.Payload.Content != "legacy message" || legacy.Payload.Model != "legacy-model" {
		t.Fatalf("legacy EventPayload compatibility changed: %#v", legacy)
	}

	event, decoded, err := DecodeExecutionEvent(legacyJSON)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := decoded.(*UserMessagePayload)
	if !ok || message.Content != "legacy message" {
		t.Fatalf("legacy execution payload = %#v", decoded)
	}
	if event.Kind != ExecutionKindUserMessage || !event.EventHeader.IsLegacy() {
		t.Fatalf("legacy execution envelope = %#v", event)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var payloadFields map[string]json.RawMessage
	var replay struct {
		Kind    ExecutionEventKind `json:"kind"`
		Payload json.RawMessage    `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replay.Payload, &payloadFields); err != nil {
		t.Fatal(err)
	}
	if replay.Kind != ExecutionKindUserMessage || string(payloadFields["role"]) != `"user"` || string(payloadFields["model"]) != `"legacy-model"` {
		t.Fatalf("legacy payload fields were not retained: %s", encoded)
	}
}

func TestUnknownExecutionKindIsPreservedAndDoesNotBreakStoreLoad(t *testing.T) {
	raw := []byte(`{"schemaVersion":99,"id":"ev-future","parentId":"","seq":3,"type":"parallel_tool_batch","ts":"2026-07-30T00:00:00Z","futureTopLevel":true,"payload":{"batchId":"batch-7","calls":[{"id":"call-1"}],"futureField":{"enabled":true}}}`)

	event, payload, err := DecodeExecutionEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	unknown, ok := payload.(*UnknownExecutionPayload)
	if !ok {
		t.Fatalf("unknown payload type = %T", payload)
	}
	if event.Kind != "parallel_tool_batch" || unknown.Kind != event.Kind {
		t.Fatalf("unknown kind was not retained: event=%q payload=%q", event.Kind, unknown.Kind)
	}
	assertJSONEqual(t, unknown.Raw, json.RawMessage(`{"batchId":"batch-7","calls":[{"id":"call-1"}],"futureField":{"enabled":true}}`))

	reencoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	replayedEvent, replayedPayload, err := DecodeExecutionEvent(reencoded)
	if err != nil {
		t.Fatal(err)
	}
	replayedUnknown, ok := replayedPayload.(*UnknownExecutionPayload)
	if !ok || replayedEvent.Kind != event.Kind {
		t.Fatalf("replayed unknown event = %#v, payload = %T", replayedEvent, replayedPayload)
	}
	assertJSONEqual(t, replayedUnknown.Raw, unknown.Raw)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("unknown event kind broke legacy Store load: %v", err)
	}
	loaded, ok := store.Event("ev-future")
	if !ok || loaded.Type != EventType("parallel_tool_batch") || loaded.SchemaVersion != 99 {
		t.Fatalf("Store did not retain unknown event identity: %#v", loaded)
	}
}

func TestExecutionEventValidationRejectsInvalidKnownPayloadsButAllowsFutureSchema(t *testing.T) {
	ts := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name  string
		event ExecutionEventEnvelope
	}{
		{
			name:  "empty delta",
			event: ExecutionEventEnvelope{ID: "ev-1", Seq: 1, TS: ts, Kind: ExecutionKindAssistantDelta, Payload: json.RawMessage(`{"delta":""}`)},
		},
		{
			name:  "tool call without id",
			event: ExecutionEventEnvelope{ID: "ev-1", Seq: 1, TS: ts, Kind: ExecutionKindToolStarted, Payload: json.RawMessage(`{"name":"read_file"}`)},
		},
		{
			name:  "condensation without source",
			event: ExecutionEventEnvelope{ID: "ev-1", Seq: 1, TS: ts, Kind: ExecutionKindContextCondensed, Payload: json.RawMessage(`{"summary":"summary"}`)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.event.Validate(); err == nil {
				t.Fatal("Validate() succeeded for invalid event")
			}
		})
	}

	future := ExecutionEventEnvelope{
		EventHeader: EventHeader{SchemaVersion: 77},
		ID:          "ev-future", Seq: 9, TS: ts, Kind: "future_kind", Payload: json.RawMessage(`{"new":true}`),
	}
	if err := future.Validate(); err != nil {
		t.Fatalf("future schema and unknown kind must remain readable: %v", err)
	}
}

func TestExecutionKindsMatchEventTypes(t *testing.T) {
	pairs := map[ExecutionEventKind]EventType{
		ExecutionKindUserMessage:        EventUserMessage,
		ExecutionKindAssistantDelta:     EventAssistantDelta,
		ExecutionKindAssistantCompleted: EventAssistantCompleted,
		ExecutionKindToolStarted:        EventToolStarted,
		ExecutionKindToolOutput:         EventToolOutput,
		ExecutionKindToolCompleted:      EventToolCompleted,
		ExecutionKindToolFailed:         EventToolFailed,
		ExecutionKindTurnInterrupted:    EventTurnInterrupted,
		ExecutionKindContextCondensed:   EventContextCondensed,
		ExecutionKindTaskTerminal:       EventTaskTerminal,
	}
	for kind, eventType := range pairs {
		if string(kind) != string(eventType) {
			t.Fatalf("execution kind %q != event type %q", kind, eventType)
		}
	}
}

func assertJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var gotValue any
	var wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch:\n got  %s\n want %s", got, want)
	}
}
