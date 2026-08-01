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

func TestDeepSeekRejectsImageAttachmentsBeforeRequest(t *testing.T) {
	provider := NewDeepSeekProvider("test-key")
	_, err := provider.Stream(context.Background(), ChatRequest{
		Model: "deepseek-chat",
		Messages: []Message{{
			Role: "user",
			Attachments: []Attachment{{
				Name: "capture.png", MIMEType: "image/png", Data: "aGVsbG8=",
			}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "不支持图片输入") {
		t.Fatalf("attachment error = %v", err)
	}
}

func TestDeepSeekListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "deepseek-v4-flash"},
				{"id": "deepseek-v4-pro"},
			},
		})
	}))
	defer server.Close()

	provider := NewDeepSeekProvider("test-key")
	provider.BaseURL = server.URL
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "deepseek-v4-flash" || models[0].DisplayName != "DeepSeek V4 Flash" {
		t.Fatalf("unexpected first model: %#v", models[0])
	}
}

func TestDeepSeekListModelsTranslatesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	provider := NewDeepSeekProvider("bad-key")
	provider.BaseURL = server.URL
	_, err := provider.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API Key 无效") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestDeepSeekStreamParsesDeltaAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\",\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12,\"prompt_cache_hit_tokens\":9,\"prompt_cache_miss_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewDeepSeekProvider("test-key")
	provider.BaseURL = server.URL
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4: %#v", len(got), got)
	}
	if got[0].Type != "reasoning" || got[0].Delta != "think" {
		t.Fatalf("first event = %#v", got[0])
	}
	if got[1].Type != "delta" || got[1].Delta != "hi" {
		t.Fatalf("second event = %#v", got[1])
	}
	if got[2].Type != "usage" || got[2].Usage.PromptCacheHitTokens != 9 {
		t.Fatalf("usage event = %#v", got[2])
	}
	if got[3].Type != "done" {
		t.Fatalf("last event = %#v", got[3])
	}
}

func TestDefaultDeepSeekHTTPClientDoesNotUseGlobalTimeout(t *testing.T) {
	client := defaultDeepSeekHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so streaming reads are not cut off", client.Timeout)
	}
}

func TestDeepSeekStreamSendsThinkingConfig(t *testing.T) {
	var payload deepSeekChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewDeepSeekProvider("test-key")
	provider.BaseURL = server.URL
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model:           "deepseek-v4-pro",
		Messages:        []Message{{Role: "user", Content: "hello"}},
		ThinkingMode:    "enabled",
		ReasoningEffort: "max",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
		t.Fatalf("thinking = %#v, want enabled", payload.Thinking)
	}
	if payload.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort = %q, want max", payload.ReasoningEffort)
	}
	if !payload.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage should be true")
	}
}

func TestParseDeepSeekStreamReportsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan StreamEvent, 1)

	parseDeepSeekStream(ctx, strings.NewReader("data: {\"choices\":[]}\n\n"), events)

	event := <-events
	if event.Type != "error" || !strings.Contains(event.Error, "上下文已取消或超时") {
		t.Fatalf("event = %#v, want context cancellation error", event)
	}
}

func TestDeepSeekStreamRetriesEOFBeforeResponse(t *testing.T) {
	calls := 0
	provider := NewDeepSeekProvider("test-key")
	provider.BaseURL = "https://api.deepseek.test"
	provider.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, io.EOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
						"data: [DONE]\n\n",
				)),
				Request: req,
			}, nil
		}),
	}

	events, err := provider.Stream(context.Background(), ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(got) != 2 || got[0].Delta != "ok" || got[1].Type != "done" {
		t.Fatalf("events = %#v", got)
	}
}

func TestDeepSeekStreamReportsEOFInChineseAfterRetries(t *testing.T) {
	provider := NewDeepSeekProvider("test-key")
	provider.BaseURL = "https://api.deepseek.test"
	provider.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, io.EOF
		}),
	}

	_, err := provider.Stream(context.Background(), ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "网络连接被中断") {
		t.Fatalf("error = %q", err.Error())
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
