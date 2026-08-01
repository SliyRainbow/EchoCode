package protocol

import (
	"context"
	"strings"
	"testing"
)

func TestParseOpenAICompatibleStreamCollectsToolCallFragments(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_","arguments":"{\"path\":\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file","arguments":"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	events := make(chan StreamEvent, 16)
	parseOpenAICompatibleStream(context.Background(), strings.NewReader(body), events)
	close(events)

	var calls []ToolCall
	for event := range events {
		if event.Type == "tool_calls" {
			calls = event.ToolCalls
		}
	}
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v, want one", calls)
	}
	if calls[0].ID != "call-1" || calls[0].Function.Name != "read_file" {
		t.Fatalf("tool call identity = %#v", calls[0])
	}
	if string(calls[0].Function.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("arguments = %s", calls[0].Function.Arguments)
	}
}
