package eventlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenReadsLegacyJSONLWithoutEventHeader(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"id":"ev-legacy","parentId":"","seq":7,"type":"user_message","ts":"2026-07-30T00:00:00Z","payload":{"role":"user","content":"legacy message"}}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	legacyEvent, ok := store.Event("ev-legacy")
	if !ok {
		t.Fatal("legacy event was not loaded")
	}
	if !legacyEvent.EventHeader.IsLegacy() {
		t.Fatalf("legacy schema version = %d, want %d", legacyEvent.SchemaVersion, LegacyEventSchemaVersion)
	}
	if legacyEvent.Seq != 7 || legacyEvent.Payload.Content != "legacy message" {
		t.Fatalf("legacy event changed during load: %#v", legacyEvent)
	}

	appended, err := store.Append(EventPayload{Content: "new message"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	if appended.SchemaVersion != CurrentEventSchemaVersion {
		t.Fatalf("new schema version = %d, want %d", appended.SchemaVersion, CurrentEventSchemaVersion)
	}
	if appended.ParentID != legacyEvent.ID || appended.Seq != 8 {
		t.Fatalf("append changed branch or sequence: %#v", appended)
	}
}

func TestAppendWithHeaderPersistsExecutionMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	header := EventHeader{
		ProjectID:   "project-7",
		SessionID:   "session-9",
		BranchID:    "branch-review",
		RunID:       "run-42",
		Generation:  7,
		CausationID: "event-parent",
		ToolCallID:  "call-write-report",
	}
	event, err := store.AppendWithHeader(header, EventPayload{Content: "created report"}, EventArtifactUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != CurrentEventSchemaVersion {
		t.Fatalf("schema version = %d, want %d", event.SchemaVersion, CurrentEventSchemaVersion)
	}
	if event.EventHeader != header.WithDefaultSchemaVersion() {
		t.Fatalf("event header = %#v, want metadata %#v", event.EventHeader, header)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schemaVersion", "projectId", "sessionId", "branchId", "runId", "generation", "causationId", "toolCallId"} {
		if _, ok := persisted[field]; !ok {
			t.Fatalf("persisted event is missing %q: %s", field, raw)
		}
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reopened.Event(event.ID)
	if !ok {
		t.Fatal("persisted event was not loaded")
	}
	if loaded.EventHeader != event.EventHeader {
		t.Fatalf("loaded header = %#v, want %#v", loaded.EventHeader, event.EventHeader)
	}
}

func TestOpenAcceptsUnknownFutureEventFields(t *testing.T) {
	dir := t.TempDir()
	future := `{"schemaVersion":99,"id":"ev-future","parentId":"","seq":3,"type":"assistant_message","ts":"2026-07-30T00:00:00Z","futureTopLevel":{"enabled":true},"payload":{"content":"future-compatible","futurePayload":"ignored safely"}}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(future+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	event, ok := store.Event("ev-future")
	if !ok {
		t.Fatal("future event was not loaded")
	}
	if event.SchemaVersion != 99 || event.Payload.Content != "future-compatible" {
		t.Fatalf("future event was not read compatibly: %#v", event)
	}
	if _, err := store.Append(EventPayload{Content: "later event"}, EventAssistantMessage); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"futureTopLevel"`) || !strings.Contains(string(raw), `"futurePayload"`) {
		t.Fatalf("append should not rewrite or discard unknown future fields: %s", raw)
	}
}

func TestBranchIDRemainsStableAcrossAppendRewindAndFork(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Append(EventPayload{Content: "first"}, EventUserMessage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append(EventPayload{Content: "second"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	if first.BranchID != first.ID || second.BranchID != first.ID || store.BranchID() != first.ID {
		t.Fatalf("original branch IDs = first:%q second:%q current:%q", first.BranchID, second.BranchID, store.BranchID())
	}

	if err := store.SetHead(first.ID); err != nil {
		t.Fatal(err)
	}
	marker, err := store.Append(EventPayload{Label: "alternative"}, EventBranchMarker)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Append(EventPayload{Content: "forked"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	if marker.BranchID != marker.ID || child.BranchID != marker.ID || store.BranchID() != marker.ID {
		t.Fatalf("fork branch IDs = marker:%q child:%q current:%q", marker.BranchID, child.BranchID, store.BranchID())
	}

	if err := store.SetHead(second.ID); err != nil {
		t.Fatal(err)
	}
	if store.BranchID() != first.ID {
		t.Fatalf("rewound original branch ID = %q, want %q", store.BranchID(), first.ID)
	}
}
