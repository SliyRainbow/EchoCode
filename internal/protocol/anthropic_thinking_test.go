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

// Empty thinking is a valid signed block, not an absent block. In particular,
// tool continuations must retain the required thinking/signature keys even
// when a gateway or an omitted-display response supplies no visible text.
func TestAnthropicEmptyThinkingSurvivesToolRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/legacy=%t", stream, legacy), func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Messages []struct {
							Role    string           `json:"role"`
							Content []map[string]any `json:"content"`
						} `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "bad request", 400)
						return
					}
					requests++
					if requests == 2 {
						if len(body.Messages) != 3 || body.Messages[1].Role != "assistant" || len(body.Messages[1].Content) != 2 {
							t.Errorf("lost assistant tool history: %#v", body.Messages)
							http.Error(w, "bad tool history", 400)
							return
						}
						thinking := body.Messages[1].Content[0]
						if thinking["type"] != "thinking" || thinking["thinking"] != "" || thinking["signature"] != "opaque-signature" {
							t.Errorf("empty signed thinking did not survive: %#v", thinking)
							http.Error(w, "missing or changed thinking", 400)
							return
						}
						for _, block := range []map[string]any{body.Messages[1].Content[1], body.Messages[2].Content[0]} {
							if _, exists := block["thinking"]; exists {
								t.Error("thinking field leaked into a tool block")
							}
							if _, exists := block["signature"]; exists {
								t.Error("signature field leaked into a tool block")
							}
						}
						if body.Messages[2].Content[0]["tool_use_id"] != "call-read" || body.Messages[2].Content[0]["content"] != "fixture" {
							t.Error("tool feedback lost")
						}
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						emit := func(data string) { _, _ = fmt.Fprintf(w, "data: %s\n\n", data) }
						if requests == 1 {
							emit(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
							emit(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-"}}`)
							emit(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signature"}}`)
							emit(`{"type":"content_block_stop","index":0}`)
							emit(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-read","name":"read_file","input":{"path":"check.txt"}}}`)
							emit(`{"type":"content_block_stop","index":1}`)
							emit(`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
						} else {
							emit(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`)
							emit(`{"type":"content_block_stop","index":0}`)
							emit(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
						}
						emit(`{"type":"message_stop"}`)
					} else {
						w.Header().Set("Content-Type", "application/json")
						if requests == 1 {
							_, _ = io.WriteString(w, `{"content":[{"type":"thinking","thinking":"","signature":"opaque-signature"},{"type":"tool_use","id":"call-read","name":"read_file","input":{"path":"check.txt"}}],"stop_reason":"tool_use"}`)
						} else {
							_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
						}
					}
				}))
				defer server.Close()
				provider := AnthropicProvider{BaseURL: server.URL, APIKey: "test-key"}
				complete := func(request ChatRequest) CompletionResult {
					t.Helper()
					if !stream {
						result, err := provider.Complete(context.Background(), request)
						if err != nil {
							t.Fatal(err)
						}
						return result
					}
					events, err := provider.Stream(context.Background(), request)
					if err != nil {
						t.Fatal(err)
					}
					var result CompletionResult
					for event := range events {
						switch event.Type {
						case "error":
							t.Error(event.Error)
						case "delta":
							result.Content += event.Delta
						case "reasoning":
							result.Reasoning += event.Delta
						case "tool_calls":
							result.ToolCalls = append(result.ToolCalls, event.ToolCalls...)
						case "continuation":
							result.Continuation = event.Continuation
						}
					}
					return result
				}
				request := ChatRequest{Model: "test-model", Messages: []Message{{Role: "user", Content: "Read check.txt"}}}
				first := complete(request)
				if first.Reasoning != "" || first.Content != "" || len(first.ToolCalls) != 1 || first.Continuation == nil {
					t.Fatalf("empty thinking should remain private while retaining the tool call: %#v", first)
				}
				// Round-trip the persisted continuation independently of its wire
				// representation. Older histories omitted the empty thinking key.
				if legacy {
					first.Continuation.Data = json.RawMessage(`[{"type":"thinking","signature":"opaque-signature"}]`)
				}
				saved, err := json.Marshal(first.Continuation)
				if err != nil {
					t.Fatal(err)
				}
				var restored ProviderContinuation
				if err := json.Unmarshal(saved, &restored); err != nil {
					t.Fatal(err)
				}
				request.Messages = append(request.Messages,
					Message{Role: "assistant", ToolCalls: first.ToolCalls, Continuation: &restored},
					Message{Role: "tool", Name: "read_file", ToolCallID: "call-read", Content: "fixture"})
				if final := complete(request); final.Content != "OK" || final.Reasoning != "" || requests != 2 {
					t.Fatalf("tool round trip failed: %#v, requests=%d", final, requests)
				}
			})
		}
	}
}

func TestAnthropicThinkingRetainsExplicitEmptySignature(t *testing.T) {
	_, messages := anthropicMessagesFromProtocol([]Message{{
		Role: "assistant", Content: "done",
		Continuation: &ProviderContinuation{Protocol: "anthropic", Data: json.RawMessage(`[{"type":"thinking","thinking":"","signature":""}]`)},
	}})
	wire, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"thinking":""`) || !strings.Contains(string(wire), `"signature":""`) {
		t.Fatalf("gateway-provided empty fields must not be dropped: %s", wire)
	}
}
