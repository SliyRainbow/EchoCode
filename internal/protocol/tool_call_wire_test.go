package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolArgumentsReachChatEndpointsAsStrings(t *testing.T) {
	raw := `{"path":"目录/with \"quotes\".txt","options":{"enabled":true}}`
	quoted, _ := json.Marshal(raw)
	for _, protocolName := range []string{"openai", "deepseek", "responses"} {
		for _, stream := range []bool{false, true} {
			for name, input := range map[string]json.RawMessage{"streamed-object": json.RawMessage(raw), "completion-string": quoted, "empty": nil} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocolName, stream, name), func(t *testing.T) {
					want := raw
					if name == "empty" {
						want = "{}"
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							http.Error(w, "invalid body", 400)
							return
						}
						var arguments any
						if protocolName == "responses" {
							for _, value := range body["input"].([]any) {
								item := value.(map[string]any)
								if item["type"] == "function_call" {
									arguments = item["arguments"]
									if item["call_id"] != "call-1" {
										t.Error("Responses changed call identity")
									}
								}
							}
						} else {
							messages := body["messages"].([]any)
							call := messages[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
							arguments = call["function"].(map[string]any)["arguments"]
							if call["id"] != "call-1" || call["type"] != "function" || messages[2].(map[string]any)["tool_call_id"] != "call-1" {
								t.Error("Chat Completions lost tool identity")
							}
						}
						value, ok := arguments.(string)
						if !ok || value != want {
							t.Errorf("wire arguments = %#v, want string %q", arguments, want)
						}
						var object map[string]any
						if err := json.Unmarshal([]byte(value), &object); err != nil {
							t.Errorf("arguments were double encoded or malformed: %s", value)
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							if protocolName == "responses" {
								_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
							} else {
								_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
							}
						} else if protocolName == "responses" {
							_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
						} else {
							_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
						}
					}))
					defer server.Close()
					var provider interface {
						Provider
						ToolCaller
					}
					if protocolName == "deepseek" {
						deepseek := NewDeepSeekProvider("test-key")
						deepseek.BaseURL = server.URL
						provider = deepseek
					} else {
						provider = OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test-key", APIType: protocolName}
					}
					request := ChatRequest{Model: "test-model", Messages: []Message{
						{Role: "user", Content: "Read the file."},
						{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Type: "function", Function: ToolCallFunction{Name: "read_file", Arguments: input}}}},
						{Role: "tool", ToolCallID: "call-1", Name: "read_file", Content: "fixture result"},
					}}
					before, _ := json.Marshal(request.Messages)
					if stream {
						events, err := provider.Stream(context.Background(), request)
						if err != nil {
							t.Fatal(err)
						}
						var content strings.Builder
						for event := range events {
							if event.Type == "error" {
								t.Error(event.Error)
							}
							if event.Type == "delta" {
								content.WriteString(event.Delta)
							}
						}
						if content.String() != "ok" {
							t.Errorf("stream content = %q", content.String())
						}
					} else {
						result, err := provider.Complete(context.Background(), request)
						if err != nil || result.Content != "ok" {
							t.Fatalf("completion = %#v, %v", result, err)
						}
					}
					after, _ := json.Marshal(request.Messages)
					if string(before) != string(after) {
						t.Fatal("wire encoding mutated the internal tool arguments")
					}
				})
			}
		}
	}
}
