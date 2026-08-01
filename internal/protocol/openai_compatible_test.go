package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAIMessagesFromProtocolSerializesImageAttachments(t *testing.T) {
	messages := openAIMessagesFromProtocol([]Message{{
		Role:    "user",
		Content: "分析图片",
		Attachments: []Attachment{{
			Name: "screen.png", MIMEType: "image/png", Data: "aGVsbG8=",
		}},
	}})
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	parts, ok := messages[0].Content.([]openAIContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v, want text + image parts", messages[0].Content)
	}
	if parts[0].Type != "text" || parts[0].Text != "分析图片" {
		t.Fatalf("text part = %#v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("image part = %#v", parts[1])
	}
}

func TestOpenAICompatibleListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4.1", "context_length": 1047576},
				{"id": "router/custom-model", "metadata": map[string]any{"max_context_length": 128000}},
			},
		})
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{
		BaseURL:    server.URL,
		APIKey:     "test-key",
		ProviderID: "openai-compatible",
	}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "gpt-4.1" || models[0].Provider != "openai-compatible" {
		t.Fatalf("unexpected first model: %#v", models[0])
	}
	if models[0].ContextWindowTokens != 1047576 || models[1].ContextWindowTokens != 128000 {
		t.Fatalf("context windows = %d / %d, want 1047576 / 128000", models[0].ContextWindowTokens, models[1].ContextWindowTokens)
	}
}

func TestOpenAICompatibleListModelsAllowsLocalNoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q, want empty", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "local-model"}},
		})
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{
		BaseURL:     server.URL,
		ProviderID:  "local-openai",
		AllowNoAuth: true,
	}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "local-model" {
		t.Fatalf("models = %#v, want local-model", models)
	}
}

func TestOpenAIUsagePreservesPromptCacheBreakdown(t *testing.T) {
	usage := openAIUsage{
		PromptTokens:     100,
		CompletionTokens: 24,
		TotalTokens:      124,
	}
	usage.PromptTokensDetails.CachedTokens = 72

	got := usage.toTokenUsage()
	if got.PromptTokens != 100 || got.CompletionTokens != 24 || got.PromptCacheHitTokens != 72 || got.PromptCacheMissTokens != 28 {
		t.Fatalf("token usage = %#v", got)
	}
}

func TestOpenAIUsageClampsInvalidCachedTokens(t *testing.T) {
	usage := openAIUsage{PromptTokens: 10}
	usage.PromptTokensDetails.CachedTokens = 24
	if got := usage.toTokenUsage(); got.PromptCacheHitTokens != 10 || got.PromptCacheMissTokens != 0 {
		t.Fatalf("token usage = %#v", got)
	}
}

func TestOpenAICompatibleStreamUsesCompatibilityOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("X-Title"); got != "EchoCode" {
			t.Fatalf("X-Title = %q, want EchoCode", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["model"] != "router/model" {
			t.Fatalf("model = %v, want protected router/model", payload["model"])
		}
		if payload["enable_thinking"] != true {
			t.Fatalf("enable_thinking = %v, want true", payload["enable_thinking"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{
		BaseURL:       server.URL,
		APIKey:        "test-key",
		ProviderID:    "router",
		ExtraHeaders:  "X-Title: EchoCode",
		ExtraBodyJSON: `{"enable_thinking":true,"model":"blocked"}`,
	}
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model:    "router/model",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for event := range events {
		if event.Type == "delta" {
			got += event.Delta
		}
	}
	if got != "ok" {
		t.Fatalf("stream content = %q, want ok", got)
	}
}

func TestOpenAICompatibleStreamRetriesTransientGatewayFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary gateway failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test-key"}
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for event := range events {
		if event.Type == "delta" {
			content += event.Delta
		}
	}
	if attempts.Load() != 2 || content != "recovered" {
		t.Fatalf("attempts=%d content=%q", attempts.Load(), content)
	}
}

func TestOpenAICompatibleDefaultClientDoesNotCapStreamDuration(t *testing.T) {
	provider := OpenAICompatibleProvider{}
	if provider.client().Timeout != 0 {
		t.Fatalf("streaming client total timeout = %s, want 0", provider.client().Timeout)
	}
}

func TestCompactOpenAICompatibleHTMLError(t *testing.T) {
	detail := compactOpenAICompatibleError(`<!DOCTYPE html><html><head><title>502 Bad gateway</title></head><body><h1>Host error</h1>` + strings.Repeat(" noisy", 200) + `</body></html>`)
	if !strings.Contains(detail, "502 Bad gateway") || !strings.Contains(detail, "Host error") || len([]rune(detail)) > 221 || strings.Contains(detail, "<!DOCTYPE") {
		t.Fatalf("detail=%q", detail)
	}
}
