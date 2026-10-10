package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type protocolInputProbe struct{ calls *int }

func (protocolInputProbe) Name() string                { return "input_probe" }
func (protocolInputProbe) Description() string         { return "Test input validation before execution." }
func (protocolInputProbe) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (p protocolInputProbe) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	*p.calls++
	return tools.Result{Summary: "executed"}, nil
}

func TestMalformedToolArgumentsDoNotExecuteAndReturnAnError(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	calls := 0
	registry := tools.NewRegistry(protocolInputProbe{calls: &calls})
	for _, input := range []string{`{"incomplete":`, `"{\"incomplete\":"`, `null`, `[]`, `"text"`} {
		result, message := service.executeToolCall(context.Background(), registry, protocol.ToolCall{ID: "bad-call", Type: "function", Function: protocol.ToolCallFunction{Name: "input_probe", Arguments: json.RawMessage(input)}})
		if !result.IsError || !message.ToolError || message.ToolCallID != "bad-call" || calls != 0 {
			t.Fatalf("bad arguments executed or error metadata lost: %s, %#v, %#v", input, result, message)
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		var restored protocol.Message
		if err := json.Unmarshal(encoded, &restored); err != nil || !restored.ToolError {
			t.Fatal("resume serialization dropped the tool error flag")
		}
	}
	result, _ := service.executeToolCall(context.Background(), registry, protocol.ToolCall{ID: "good-call", Type: "function", Function: protocol.ToolCallFunction{Name: "input_probe", Arguments: json.RawMessage(`{}`)}})
	if result.IsError || calls != 1 {
		t.Fatal("valid empty object was rejected")
	}
}

func TestStructuredProviderFailureControlsRetry(t *testing.T) {
	blocked := protocol.NewProviderError(protocol.ProviderErrorInfo{Code: "content_filter", Message: "上游中断输出", Retryable: false})
	if isRetryablePostToolCompletionError(context.Background(), blocked) {
		t.Fatal("non-retryable failure was retried due to a text keyword")
	}
	transient := protocol.NewProviderError(protocol.ProviderErrorInfo{Code: "overloaded_error", Message: "temporarily busy", Retryable: true})
	if !isRetryablePostToolCompletionError(context.Background(), transient) {
		t.Fatal("retryable stream failure ignored its structured status")
	}
}
