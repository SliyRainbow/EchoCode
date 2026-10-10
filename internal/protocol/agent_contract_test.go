package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func auditStreamEvents(parser func(context.Context, io.Reader, chan<- StreamEvent), data string) []StreamEvent {
	events := make(chan StreamEvent, 128)
	parser(context.Background(), strings.NewReader(data), events)
	close(events)
	var result []StreamEvent
	for event := range events {
		result = append(result, event)
	}
	return result
}

func TestProtocolStreamsRejectPrematureEOF(t *testing.T) {
	for _, test := range []struct {
		name, data string
		parse      func(context.Context, io.Reader, chan<- StreamEvent)
	}{
		{"chat", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", parseOpenAICompatibleStream},
		{"deepseek", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", parseDeepSeekStream},
		{"messages", "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"partial\"}}\n\n", parseAnthropicStream},
		{"responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", func(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
			parseResponsesStream(ctx, body, events, "test", responsesTransportMetadata{})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := auditStreamEvents(test.parse, test.data)
			text, failed := "", false
			for _, event := range events {
				if event.Type == "delta" {
					text += event.Delta
				}
				if event.Type == "done" || event.Type == "tool_calls" {
					t.Errorf("truncated response emitted %s", event.Type)
				}
				if event.ProviderError != nil && event.ProviderError.Code == "unexpected_eof" && event.ProviderError.Retryable {
					failed = true
				}
			}
			if text != "partial" || !failed {
				t.Fatalf("partial output/failure lost: %#v", events)
			}
		})
	}
}

func TestGPT6ToolAPIConstraintsAreScopedToTheOfficialEndpoint(t *testing.T) {
	tools := []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{Name: "read_file"}}}
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		request := ChatRequest{Model: model, Tools: tools, ReasoningLevel: "high"}
		official := OpenAICompatibleProvider{BaseURL: "https://api.openai.com/v1"}
		err := official.validateChatToolSupport(request, ReasoningOptions{Effort: "high"})
		if info, ok := ProviderErrorDetails(err); !ok || info.Code != "responses_required" {
			t.Fatalf("missing API-type guidance for %s: %v", model, err)
		}
		relay := OpenAICompatibleProvider{BaseURL: "https://relay.example/v1"}
		if err := relay.validateChatToolSupport(request, ReasoningOptions{Effort: "high"}); err != nil {
			t.Fatal("a relay's translation capability was assumed away")
		}
		if model == "gpt-6-sol" || model == "gpt-6-luna" {
			if err := official.validateChatToolSupport(request, ReasoningOptions{Effort: "none"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestChatStreamsHandleMultilineSSEAndTerminalFailures(t *testing.T) {
	for name, parser := range map[string]func(context.Context, io.Reader, chan<- StreamEvent){"chat": parseOpenAICompatibleStream, "deepseek": parseDeepSeekStream} {
		t.Run(name, func(t *testing.T) {
			events := auditStreamEvents(parser, ": keepalive\r\nevent: message\r\ndata: {\"choices\":\r\ndata: [{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n")
			if events[0].Type != "delta" || events[0].Delta != "hello" || events[len(events)-1].Type != "done" {
				t.Fatalf("multiline SSE failed: %#v", events)
			}
			for reason, code := range map[string]string{"length": "max_output_tokens", "content_filter": "content_filter"} {
				data := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"" + reason + "\"}]}\n\ndata: [DONE]\n\n"
				events = auditStreamEvents(parser, data)
				last := events[len(events)-1]
				if last.Type != "error" || last.ProviderError == nil || last.ProviderError.Code != code || last.ProviderError.Retryable {
					t.Fatalf("%s incorrectly completed: %#v", reason, events)
				}
			}
			events = auditStreamEvents(parser, "data: {\"error\":{\"type\":\"rate_limit_exceeded\",\"message\":\"wait before retry\"}}\n\n")
			if len(events) != 1 || events[0].ProviderError == nil || !events[0].ProviderError.Retryable {
				t.Fatalf("stream error was ignored: %#v", events)
			}
		})
	}
}

func TestResponsesSummaryOrderedToolsAndReplay(t *testing.T) {
	frames := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs-1"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Check "}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"inputs"}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"Check inputs"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs-1","summary":[{"type":"summary_text","text":"Check inputs"}],"encrypted_content":"opaque-cipher"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg-1","role":"assistant","phase":"commentary"}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"Inspecting."}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg-1","content":[{"type":"output_text","text":"Inspecting."}]}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc-z","call_id":"call-z","name":"list_dir","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{"}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":2,"arguments":"{}"}`,
		`{"type":"response.output_item.done","output_index":3,"item":{"type":"function_call","id":"fc-a","call_id":"call-a","name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
	}
	for index := range frames {
		frames[index] = "data: " + frames[index]
	}
	events := auditStreamEvents(func(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
		parseResponsesStream(ctx, body, events, "gpt-test", responsesTransportMetadata{})
	}, strings.Join(frames, "\n\n")+"\n\n")
	var text, reasoning string
	var calls []ToolCall
	var continuation *ProviderContinuation
	for _, event := range events {
		switch event.Type {
		case "delta":
			text += event.Delta
		case "reasoning":
			reasoning += event.Delta
		case "tool_calls":
			calls = append(calls, event.ToolCalls...)
		case "continuation":
			continuation = event.Continuation
		case "error":
			t.Error(event.Error)
		}
	}
	if text != "Inspecting." || reasoning != "Check inputs" || len(calls) != 2 || calls[0].ID != "call-z" || calls[1].ID != "call-a" {
		t.Fatalf("ordered output/deduplication failed: text=%q reasoning=%q calls=%#v", text, reasoning, calls)
	}
	_, input := responsesInputFromProtocol([]Message{{Role: "assistant", Content: text, ToolCalls: calls, Continuation: continuation}})
	if len(input) != 4 || input[0].EncryptedContent != "opaque-cipher" || input[1].Phase != "commentary" || input[2].CallID != "call-z" {
		t.Fatalf("continuation lost replay state: %#v", input)
	}
	_, clipped := responsesInputFromProtocol([]Message{{Role: "assistant", Content: "short", ToolCalls: calls, Continuation: continuation}})
	if len(clipped) != 4 || clipped[0].EncryptedContent != "opaque-cipher" || clipped[1].Content[0].Text != "short" || clipped[1].Phase != "commentary" {
		t.Fatalf("replay ignored context clipping: %#v", clipped)
	}
}

func TestResponsesExplicitNonStrictSchemaAndEmptyToolOutput(t *testing.T) {
	provider := OpenAICompatibleProvider{ClientVersion: "test"}
	body := provider.responsesRequest(ChatRequest{Model: "gpt-6-astra", ReasoningLevel: "high", SessionID: "session", MaxOutputTokens: 512, Messages: []Message{{Role: "tool", ToolCallID: "call-1", Content: ""}}, Tools: []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{Name: "optional", Parameters: map[string]any{"type": "object", "required": []string{}}}}}}, false)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	_ = json.Unmarshal(encoded, &wire)
	if _, exists := wire["client_metadata"]; exists {
		t.Fatal("private body extension leaked into public Responses request")
	}
	if wire["tools"].([]any)[0].(map[string]any)["strict"] != false || wire["input"].([]any)[0].(map[string]any)["output"] != "" {
		t.Fatalf("optional schema or empty result changed: %s", encoded)
	}
	if wire["reasoning"].(map[string]any)["summary"] != "auto" || wire["max_output_tokens"] != float64(512) {
		t.Fatalf("response options missing: %s", encoded)
	}
	encoded, _ = json.Marshal(responsesInputItem{Type: "reasoning", ID: "rs-empty", EncryptedContent: "opaque"})
	if !strings.Contains(string(encoded), `"summary":[]`) {
		t.Fatalf("empty reasoning summary omitted: %s", encoded)
	}
}

func TestResponsesIncompleteResponseKeepsPartialTextAndUsage(t *testing.T) {
	data := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"total_tokens\":30}}}\n\n"
	events := auditStreamEvents(func(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
		parseResponsesStream(ctx, body, events, "gpt-test", responsesTransportMetadata{})
	}, data)
	last := events[len(events)-1]
	if events[0].Delta != "partial" || last.ProviderError == nil || last.ProviderError.Code != "max_output_tokens" {
		t.Fatalf("incomplete response was lost: %#v", events)
	}
	foundUsage := false
	for _, event := range events {
		if event.Type == "done" {
			t.Fatal("incomplete response reported success")
		}
		if event.Usage != nil && event.Usage.TotalTokens == 30 {
			foundUsage = true
		}
	}
	if !foundUsage {
		t.Fatal("incomplete response usage was lost")
	}
}

func TestAnthropicGroupsToolResultsAndKeepsErrorAndThinking(t *testing.T) {
	result := anthropicCompletionFromBlocks([]anthropicContentBlock{{Type: "thinking", Thinking: "summary", Signature: "opaque-signature"}, {Type: "tool_use", ID: "call-1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}, {Type: "tool_use", ID: "call-2", Name: "read_file", Input: json.RawMessage(`{"path":"b"}`)}})
	_, messages := anthropicMessagesFromProtocol([]Message{
		{Role: "assistant", ToolCalls: result.ToolCalls, Continuation: result.Continuation},
		{Role: "tool", ToolCallID: "call-1", Content: "read denied", ToolError: true},
		{Role: "tool", ToolCallID: "call-2", Content: "file contents"},
	})
	if len(messages) != 2 || len(messages[1].Content) != 2 || !messages[1].Content[0].IsError || messages[1].Content[1].ToolUseID != "call-2" || messages[0].Content[0].Signature != "opaque-signature" {
		t.Fatalf("native tool result grouping failed: %#v", messages)
	}
	data := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":8,\"cache_creation_input_tokens\":4}}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	events := auditStreamEvents(parseAnthropicStream, data)
	usage := events[len(events)-2].Usage
	if usage == nil || usage.PromptTokens != 24 || usage.TotalTokens != 27 || usage.PromptCacheHitTokens != 8 || usage.PromptCacheMissTokens != 4 {
		t.Fatalf("cache and cumulative usage lost: %#v", events)
	}
}

func TestChatToolImagesFollowAllToolResults(t *testing.T) {
	messages := openAIMessagesFromProtocol([]Message{
		{Role: "tool", ToolCallID: "a", Content: "first", Attachments: []Attachment{{MIMEType: "image/png", Data: "aGVsbG8="}}},
		{Role: "tool", ToolCallID: "b", Content: "second"},
	})
	if len(messages) != 3 || messages[0].Content != "first" || messages[1].Role != "tool" || messages[2].Role != "user" {
		t.Fatalf("tool images interrupted the result group: %#v", messages)
	}
}

func TestToolChoiceAndParallelControlsReachWire(t *testing.T) {
	for _, dialect := range []string{"chat", "deepseek", "responses", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(dialect+map[bool]string{true: "/stream", false: "/complete"}[stream], func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if dialect == "messages" {
						choice, _ := body["tool_choice"].(map[string]any)
						if choice["type"] != "any" || choice["disable_parallel_tool_use"] != true {
							t.Errorf("native tool choice = %#v", choice)
						}
					} else if body["tool_choice"] != "required" || body["parallel_tool_calls"] != false {
						t.Errorf("tool controls = %#v", body)
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						switch dialect {
						case "messages":
							_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
						case "responses":
							_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
						default:
							_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
						}
					} else {
						switch dialect {
						case "messages":
							_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
						case "responses":
							_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
						default:
							_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
						}
					}
				}))
				defer server.Close()
				var provider interface {
					Provider
					ToolCaller
				}
				switch dialect {
				case "messages":
					provider = AnthropicProvider{BaseURL: server.URL, APIKey: "test"}
				case "deepseek":
					ds := NewDeepSeekProvider("test")
					ds.BaseURL = server.URL
					provider = ds
				default:
					provider = OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: dialect}
				}
				request := ChatRequest{Model: "test-model", ToolChoice: "required", ParallelToolCalls: false, MaxOutputTokens: 4096, Messages: []Message{{Role: "user", Content: "use a tool"}}, Tools: []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{Name: "read_file", Parameters: map[string]any{"type": "object"}}}}}
				if stream {
					events, err := provider.Stream(context.Background(), request)
					if err != nil {
						t.Fatal(err)
					}
					for event := range events {
						if event.Type == "error" {
							t.Error(event.Error)
						}
					}
				} else {
					if _, err := provider.Complete(context.Background(), request); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
