package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeepSeekEmptyReasoningSurvivesChatToolReplay(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/stream=%t", native, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Messages []map[string]json.RawMessage `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					for _, message := range body.Messages {
						if string(message["role"]) == `"assistant"` && string(message["reasoning_content"]) != `""` {
							t.Errorf("empty assistant reasoning omitted: %s", message["reasoning_content"])
						}
						if string(message["role"]) == `"tool"` {
							if _, exists := message["reasoning_content"]; exists {
								t.Error("tool result acquired reasoning field")
							}
						}
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()
				request := ChatRequest{Model: "deepseek-v4-flash", ReasoningLevel: "low", Messages: []Message{{Role: "assistant", Content: "previous final answer"}, {Role: "user", Content: "continue"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: ToolCallFunction{Name: "browser", Arguments: json.RawMessage(`{}`)}}}}, {Role: "tool", ToolCallID: "c1", Content: "done"}}, Tools: []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{Name: "browser"}}}}
				var p Provider = OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "fixture"}
				if native {
					p = DeepSeekProvider{BaseURL: server.URL, APIKey: "fixture"}
				}
				if stream {
					events, err := p.Stream(context.Background(), request)
					if err != nil {
						t.Fatal(err)
					}
					for event := range events {
						if event.Type == "error" {
							t.Fatal(event.Error)
						}
					}
				} else {
					if _, err := p.(ToolCaller).Complete(context.Background(), request); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestOrdinaryChatDoesNotAcquireEmptyReasoningFields(t *testing.T) {
	request := ChatRequest{Model: "generic-model", Tools: []ToolDefinition{{Type: "function"}}}
	if requiresDeepSeekReasoningReplay("auto", request) {
		t.Fatal("unrelated model matched DeepSeek replay")
	}
	data, _ := json.Marshal(openAIMessagesFromProtocol([]Message{{Role: "assistant", Content: "hello"}}))
	var messages []map[string]any
	_ = json.Unmarshal(data, &messages)
	if _, ok := messages[0]["reasoning_content"]; ok {
		t.Fatal("unsolicited reasoning field")
	}
}
