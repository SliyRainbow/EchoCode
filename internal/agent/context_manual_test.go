package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

func manualContextFixture(t *testing.T) (*Service, ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(root, "sessions"), SettingsPath: filepath.Join(root, "settings.json")}
	s := NewService(config)
	t.Cleanup(s.Close)
	settings := s.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{SelectedProviderID: "fixture", SelectedModelID: "large", Providers: []ModelProviderSetting{{ID: "fixture", Name: "Fixture", Enabled: true, Protocol: "local", DefaultModelID: "large", Models: []ProviderModel{{ID: "large", ContextWindowTokens: 64000, ContextWindowSource: ContextWindowSourceManual}}}}}
	if _, err := s.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	s.sessionMessages = []protocol.Message{{Role: "system", Content: "stable"}}
	for i := 0; i < 10; i++ {
		user, answer := strings.Repeat("request ", 240), strings.Repeat("answer ", 240)
		if err := s.recordUserEvent(user); err != nil {
			t.Fatal(err)
		}
		s.recordAssistantAndCheckpoint(answer, "large", nil)
		s.sessionMessages = append(s.sessionMessages, protocol.Message{Role: "user", Content: user}, protocol.Message{Role: "assistant", Content: answer})
	}
	return s, config
}

func TestManualContextCompressionBelowThresholdAndRestart(t *testing.T) {
	s, config := manualContextFixture(t)
	before := s.WorkbenchState().DeepSeekSession
	if before.EstimatedInputTokens >= before.CompressionTriggerTokens {
		t.Fatal("fixture must be below automatic threshold")
	}
	history, checkpoints := s.GetSessionMessages(), s.ListCheckpoints()
	p, id := s.ActiveSessionIDs()
	result, err := s.CompressSessionContext(p, id)
	if err != nil || !result.Compressed || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("manual compression: %+v, %v", result, err)
	}
	if !reflect.DeepEqual(history, s.GetSessionMessages()) || !reflect.DeepEqual(checkpoints, s.ListCheckpoints()) {
		t.Fatal("compression changed visible history or file checkpoints")
	}
	want := cloneProtocolMessages(s.sessionMessages[1:])
	s.Close()
	restored := NewService(config)
	defer restored.Close()
	if !reflect.DeepEqual(want, restored.sessionMessages[1:]) {
		t.Fatal("restart did not restore compacted context")
	}
	state := restored.WorkbenchState().DeepSeekSession
	if state.CompressionCount != 1 || state.ContextWindowTokens != 64000 || state.EstimatedInputTokens == 0 || state.LastCompressedAt == "" {
		t.Fatalf("restored telemetry: %+v", state)
	}
	if !reflect.DeepEqual(history, restored.GetSessionMessages()) {
		t.Fatal("restart changed raw chat history")
	}
}

func TestManualContextCompressionNoOpBusyAndWrongSession(t *testing.T) {
	s, _ := manualContextFixture(t)
	p, id := s.ActiveSessionIDs()
	s.sessionMessages = []protocol.Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}}
	before := s.eventStore.Head()
	result, err := s.CompressSessionContext(p, id)
	if err != nil || result.Compressed || s.sessionState.CompressionCount != 0 || s.eventStore.Head() != before {
		t.Fatalf("short context must be a no-op: %+v %v", result, err)
	}
	if _, err := s.CompressSessionContext(p, "other"); err == nil {
		t.Fatal("wrong session accepted")
	}
	release, err := s.beginActivity("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompressSessionContext(p, id); err == nil {
		t.Fatal("busy compression accepted")
	}
	release()
	// A model change should refresh the displayed budget without sending a turn.
	s.runtimeSettings.Model.Providers[0].Models[0].ContextWindowTokens = 32000
	if got := s.WorkbenchState().DeepSeekSession.ContextWindowTokens; got != 32000 {
		t.Fatalf("stale model window: %d", got)
	}
}

func TestManualContextCompressionPersistenceFailureRollsBack(t *testing.T) {
	s, config := manualContextFixture(t)
	p, id := s.ActiveSessionIDs()
	before, beforeState, head := cloneProtocolMessages(s.sessionMessages), s.sessionState, s.eventStore.Head()
	path := filepath.Join(config.SessionsDir, p, id, "HEAD")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompressSessionContext(p, id); err == nil {
		t.Fatal("persistence failure hidden")
	}
	if !reflect.DeepEqual(before, s.sessionMessages) || !reflect.DeepEqual(beforeState, s.sessionState) || head != s.eventStore.Head() {
		t.Fatal("failed compression mutated session")
	}
}

func TestContextViewPreservesToolFailureAndSignedContinuation(t *testing.T) {
	messages := []protocol.Message{
		{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: "call-1", Function: protocol.ToolCallFunction{Name: "read_file", Arguments: json.RawMessage(`{}`)}}}, Continuation: &protocol.ProviderContinuation{Protocol: "anthropic", Data: json.RawMessage(`[{"type":"thinking","thinking":"","signature":"signed-state"}]`)}},
		{Role: "tool", Name: "read_file", ToolCallID: "call-1", Content: "permission denied", ToolError: true},
	}
	data, err := json.Marshal(encodePersistedContextView(messages))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePersistedContextView(data)
	if err != nil || !reflect.DeepEqual(messages, got) {
		t.Fatalf("transport context changed: %#v, %v", got, err)
	}
}
