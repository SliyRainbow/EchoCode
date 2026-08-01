package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiContentsFromProtocolSerializesImageAttachments(t *testing.T) {
	system, contents := geminiContentsFromProtocol([]Message{{
		Role:    "user",
		Content: "分析图片",
		Attachments: []Attachment{{
			Name: "screen.jpg", MIMEType: "image/jpeg", Data: "aGVsbG8=",
		}},
	}})
	if system != "" || len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("system=%q contents=%#v", system, contents)
	}
	if contents[0].Parts[0].Text != "分析图片" {
		t.Fatalf("text part = %#v", contents[0].Parts[0])
	}
	image := contents[0].Parts[1].InlineData
	if image == nil || image.MIMEType != "image/jpeg" || image.Data != "aGVsbG8=" {
		t.Fatalf("inline image = %#v", image)
	}
}

func TestGeminiListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		if got := r.URL.Query().Get("key"); got != "" {
			t.Fatalf("API key leaked into query: %q", got)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Fatalf("x-goog-api-key = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name":                       "models/gemini-2.5-flash",
					"displayName":                "Gemini 2.5 Flash",
					"inputTokenLimit":            1048576,
					"supportedGenerationMethods": []string{"generateContent"},
				},
				{
					"name":                       "models/embedding-001",
					"displayName":                "Embedding",
					"supportedGenerationMethods": []string{"embedContent"},
				},
			},
		})
	}))
	defer server.Close()

	provider := GeminiProvider{BaseURL: server.URL, APIKey: "test-key", ProviderID: "gemini"}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1: %#v", len(models), models)
	}
	if models[0].ID != "gemini-2.5-flash" || models[0].ContextWindowTokens != 1048576 {
		t.Fatalf("model = %#v", models[0])
	}
}

func TestGeminiStreamParsesDeltaAndUsage(t *testing.T) {
	var payload geminiGenerateContentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-2.5-flash:streamGenerateContent" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Fatalf("alt = %q, want sse", r.URL.Query().Get("alt"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"totalTokenCount\":12,\"cachedContentTokenCount\":7}}\n\n"))
	}))
	defer server.Close()

	provider := GeminiProvider{BaseURL: server.URL, APIKey: "test-key"}
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "previous"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if payload.SystemInstruction == nil || payload.SystemInstruction.Parts[0].Text != "system" {
		t.Fatalf("system instruction = %#v", payload.SystemInstruction)
	}
	if len(payload.Contents) != 2 || payload.Contents[1].Role != "model" {
		t.Fatalf("contents = %#v", payload.Contents)
	}
	if len(got) != 3 {
		t.Fatalf("events = %#v, want delta/usage/done", got)
	}
	if got[0].Type != "delta" || got[0].Delta != "hi" {
		t.Fatalf("delta = %#v", got[0])
	}
	if got[1].Type != "usage" || got[1].Usage.PromptCacheHitTokens != 7 || got[1].Usage.PromptCacheMissTokens != 3 {
		t.Fatalf("usage = %#v", got[1])
	}
	if got[2].Type != "done" {
		t.Fatalf("last = %#v", got[2])
	}
}

func TestGeminiStreamTreatsFinishReasonAsTerminal(t *testing.T) {
	events := make(chan StreamEvent, 8)
	parseGeminiStream(context.Background(), strings.NewReader(
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":1,\"totalTokenCount\":10}}\n\n",
	), events)
	close(events)

	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 4 || got[0].Type != "delta" || got[1].Type != "usage" || got[2].Type != "finish" || got[3].Type != "done" {
		t.Fatalf("events = %#v, want delta/usage/finish/done", got)
	}
	if got[2].FinishReason != "STOP" {
		t.Fatalf("finish reason = %q", got[2].FinishReason)
	}
}

func TestGeminiContentsJoinStablePromptAndCompressedMemory(t *testing.T) {
	system, contents := geminiContentsFromProtocol([]Message{
		{Role: "system", Content: "stable prompt"},
		{Role: "system", Content: "compressed memory"},
		{Role: "user", Content: "continue"},
	})
	if system != "stable prompt\n\ncompressed memory" {
		t.Fatalf("system = %q", system)
	}
	if len(contents) != 1 || contents[0].Role != "user" {
		t.Fatalf("contents = %#v", contents)
	}
}

func TestGeminiErrorIncludesMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer server.Close()

	provider := GeminiProvider{BaseURL: server.URL, APIKey: "test-key"}
	_, err := provider.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %v, want bad request", err)
	}
}

func TestGeminiCompleteUsesNativeFunctionCalls(t *testing.T) {
	var payload geminiGenerateContentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-2.5-pro:generateContent" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{
					{"text": "checking"},
					{"functionCall": map[string]any{"name": "read_file", "args": map[string]any{"path": "README.md"}}},
				}},
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 18, "candidatesTokenCount": 3, "totalTokenCount": 21},
		})
	}))
	defer server.Close()

	provider := GeminiProvider{BaseURL: server.URL, APIKey: "test-key"}
	result, err := provider.Complete(context.Background(), ChatRequest{
		Model: "gemini-2.5-pro",
		Messages: []Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Type: "function", Function: ToolCallFunction{Name: "list_dir", Arguments: json.RawMessage(`{"path":"."}`)}}}},
			{Role: "tool", Name: "list_dir", ToolCallID: "call-1", Content: `{"entries":["README.md"]}`},
		},
		Tools: []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{
			Name: "read_file", Description: "Read a file", Parameters: map[string]any{"type": "object"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Tools) != 1 || len(payload.Tools[0].FunctionDeclarations) != 1 || payload.Tools[0].FunctionDeclarations[0].Name != "read_file" {
		t.Fatalf("tools = %#v", payload.Tools)
	}
	if len(payload.Contents) != 3 || payload.Contents[1].Parts[0].FunctionCall == nil || payload.Contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("contents = %#v", payload.Contents)
	}
	if result.Content != "checking" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("result = %#v", result)
	}
	if string(result.ToolCalls[0].Function.Arguments) != `{"path":"README.md"}` || result.Usage == nil || result.Usage.PromptTokens != 18 {
		t.Fatalf("result details = %#v", result)
	}
}

func TestGeminiStreamEmitsNativeFunctionCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}}]}}]}\n\n"))
	}))
	defer server.Close()

	provider := GeminiProvider{BaseURL: server.URL, APIKey: "test-key"}
	events, err := provider.Stream(context.Background(), ChatRequest{Model: "gemini-2.5-pro", Messages: []Message{{Role: "user", Content: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 || got[0].Type != "tool_calls" || got[1].Type != "done" {
		t.Fatalf("events = %#v", got)
	}
	if len(got[0].ToolCalls) != 1 || string(got[0].ToolCalls[0].Function.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool call = %#v", got[0].ToolCalls)
	}
}
