package protocol

import (
	"encoding/json"
	"strings"
)

// Internal tool arguments are raw JSON for host execution. Chat Completions
// requires function.arguments to be a JSON-encoded string when assistant tool
// calls are sent back alongside their results. Non-streaming upstreams may
// already wrap that JSON in a string, so unwrap once before encoding the wire.
type chatCompletionToolCall struct {
	ID       string                     `json:"id"`
	Type     string                     `json:"type"`
	Function chatCompletionToolFunction `json:"function"`
}

type chatCompletionToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func chatCompletionToolCalls(calls []ToolCall) []chatCompletionToolCall {
	if len(calls) == 0 {
		return nil
	}
	result := make([]chatCompletionToolCall, len(calls))
	for index, call := range calls {
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		result[index] = chatCompletionToolCall{ID: call.ID, Type: callType, Function: chatCompletionToolFunction{Name: call.Function.Name, Arguments: toolCallArgumentsString(call.Function.Arguments)}}
	}
	return result
}

func toolCallArgumentsString(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "{}"
	}
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if json.Unmarshal(raw, &decoded) == nil {
			return decoded
		}
	}
	return value
}

// Keep malformed model output recoverable instead of replacing it with an
// executable empty object. The host rejects it and returns a tool error.
func preserveToolArguments(value string) json.RawMessage {
	if strings.TrimSpace(value) == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
