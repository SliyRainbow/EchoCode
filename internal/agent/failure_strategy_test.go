package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func deterministicFailureFixture(call protocol.ToolCall) tools.Result {
	exitCode := 1
	return tools.Result{
		Summary: "command failed",
		IsError: true,
		Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: call.Function.Name, Status: "error",
			Output: "Access is denied.", Stderr: "Access is denied.", ExitCode: &exitCode,
		}},
	}
}

func TestFailureStrategyPersistsAcrossRestartWithoutRawArguments(t *testing.T) {
	sessionsDir := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessionsDir}
	service := NewService(config)
	call := protocol.ToolCall{
		ID: "secret-command",
		Function: protocol.ToolCallFunction{
			Name:      "run_command",
			Arguments: json.RawMessage(`{"command":"deploy --password do-not-persist --target D:\\Site"}`),
		},
	}
	state := service.failureStrategySnapshot()
	record, _ := state.observeFailure(call, deterministicFailureFixture(call), 1)
	if record.StrategyKey == "" {
		t.Fatal("failure strategy was not recorded")
	}
	service.replaceFailureStrategyState(state)
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("deployment failed", "test-model", nil)
	service.Close()

	var persisted strings.Builder
	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || filepath.Base(path) != "events.jsonl" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		persisted.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted.String(), "do-not-persist") || strings.Contains(persisted.String(), "deploy --password") {
		t.Fatalf("raw command or password leaked into durable failure state: %s", persisted.String())
	}

	restored := NewService(config)
	defer restored.Close()
	restoredState := restored.failureStrategySnapshot()
	if len(restoredState.Records) != 1 || restoredState.Records[0].StrategyKey != record.StrategyKey {
		t.Fatalf("restored failure state = %#v", restoredState)
	}
	foundContext := false
	for _, message := range restored.sessionMessages {
		if message.InternalKind != contextFailureStrategyKind {
			continue
		}
		foundContext = true
		if strings.Contains(message.Content, "do-not-persist") || !strings.Contains(message.Content, "access-denied") {
			t.Fatalf("restored private context = %q", message.Content)
		}
	}
	if !foundContext {
		t.Fatal("restored session did not receive branch-local failure context")
	}

	guard := toolLoopGuard{failureStrategy: restoredState, turnIndex: 2, blockedFailures: map[string]int{}}
	_, message, blocked, _ := guard.before(call)
	if !blocked || !strings.Contains(message.Content, "blocked_equivalent_retry") {
		t.Fatalf("restored strategy was not blocked: blocked=%v message=%q", blocked, message.Content)
	}
}

func TestFailureStrategyFollowsRewindBranch(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	call := protocol.ToolCall{
		ID:       "failed-command",
		Function: protocol.ToolCallFunction{Name: "run_command", Arguments: json.RawMessage(`{"command":"deploy D:\\Site"}`)},
	}
	state := service.failureStrategySnapshot()
	state.observeFailure(call, deterministicFailureFixture(call), 1)
	service.replaceFailureStrategyState(state)
	service.recordUserEvent("deploy")
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("deployment failed", "test-model", nil)
	checkpoints := service.ListCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("checkpoints = %#v", checkpoints)
	}

	resolved := map[string]bool{}
	state = service.failureStrategySnapshot()
	state.observeSuccess(call, tools.Result{Summary: "deployment completed"}, resolved)
	service.replaceFailureStrategyState(state)
	service.recordUserEvent("retry after fixing permissions")
	service.sessionState.TurnCount = 2
	service.recordAssistantAndCheckpoint("deployment completed", "test-model", nil)
	if current := service.failureStrategySnapshot(); len(current.Records) != 0 {
		t.Fatalf("resolved branch still has failures: %#v", current)
	}

	if _, err := service.RewindToCheckpoint(checkpoints[0].ID); err != nil {
		t.Fatal(err)
	}
	rewound := service.failureStrategySnapshot()
	if len(rewound.Records) != 1 || rewound.Records[0].FailureClass != "access-denied" {
		t.Fatalf("rewound failure state = %#v", rewound)
	}
}

func TestContextCompressionKeepsOnlyLatestFailureStrategyContext(t *testing.T) {
	oldState := failureStrategyState{Revision: 1, Records: []failureStrategyRecord{{
		StrategyKey: "old", Tool: "run_command", Category: "shell", FailureClass: "syntax-error", Attempts: 1,
	}}}
	latestState := failureStrategyState{Revision: 2, Records: []failureStrategyRecord{{
		StrategyKey: "latest", Tool: "spreadsheet_create", Category: "spreadsheet", FailureClass: "data-invalid", Attempts: 1,
	}}}
	messages := []protocol.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: strings.Repeat("first request ", 80)},
		{Role: "assistant", Content: "first result"},
		{Role: "system", InternalKind: contextFailureStrategyKind, Content: formatFailureStrategyContext(oldState)},
		{Role: "user", Content: strings.Repeat("second request ", 80)},
		{Role: "assistant", Content: "second result"},
		{Role: "system", InternalKind: contextFailureStrategyKind, Content: formatFailureStrategyContext(latestState)},
		{Role: "user", Content: "continue"},
	}
	compressed, removed := compressProtocolMessages(messages, contextBudget{TargetTokens: 700})
	if removed == 0 {
		t.Fatal("test fixture did not trigger message removal")
	}
	contexts := 0
	for _, message := range compressed {
		if message.InternalKind == contextSummaryKind && strings.Contains(message.Content, failureStrategyContextStart) {
			t.Fatalf("failure strategy leaked into compressed prose: %q", message.Content)
		}
		if message.InternalKind != contextFailureStrategyKind {
			continue
		}
		contexts++
		if !strings.Contains(message.Content, "strategy=latest") || strings.Contains(message.Content, "strategy=old") {
			t.Fatalf("compressed failure context = %q", message.Content)
		}
	}
	if contexts != 1 {
		t.Fatalf("failure contexts after compression = %d; messages=%#v", contexts, compressed)
	}
}

func TestTransientFailureGetsOneEquivalentRetry(t *testing.T) {
	call := protocol.ToolCall{ID: "network", Function: protocol.ToolCallFunction{Name: "read_webpage", Arguments: json.RawMessage(`{"url":"https://example.com"}`)}}
	result := tools.Result{Summary: "connection reset by peer", IsError: true, Parts: []tools.ResultPart{{Kind: tools.PartToolCall, Output: "connection reset by peer"}}}
	state := failureStrategyState{}
	state.observeFailure(call, result, 1)
	if _, blocked := state.equivalentFailure(call, 1); blocked {
		t.Fatal("first transient failure should permit one equivalent retry")
	}
	state.observeFailure(call, result, 1)
	if _, blocked := state.equivalentFailure(call, 1); !blocked {
		t.Fatal("second transient failure should require a different strategy")
	}
}

func TestNetworkFailurePreservesSpecificEvidencePath(t *testing.T) {
	networkFailure := tools.Result{
		Summary: "connection reset by peer",
		IsError: true,
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Output: "connection reset by peer"}},
	}
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{name: "webpage", tool: "read_webpage", args: `{"url":"https://example.com"}`, want: "browser"},
		{name: "repository", tool: "read_repository", args: `{"url":"https://github.com/example/project"}`, want: "git_repository"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			diagnosis := diagnoseToolFailure(protocol.ToolCall{
				Function: protocol.ToolCallFunction{Name: test.tool, Arguments: json.RawMessage(test.args)},
			}, networkFailure)
			if !containsString(diagnosis.Alternatives, test.want) {
				t.Fatalf("alternatives=%#v, want %q", diagnosis.Alternatives, test.want)
			}
			if containsString(diagnosis.Alternatives, "web_search") {
				t.Fatalf("specific evidence failure fell back to web_search: %#v", diagnosis.Alternatives)
			}
		})
	}
}
