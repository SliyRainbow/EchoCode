package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResponsesCompleteUsesNativeEndpointAndTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "gpt-test" || request.Instructions != "system rules" || len(request.Tools) != 1 {
			t.Fatalf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "output": [
    {"type":"message","content":[{"type":"output_text","text":"done"}]},
    {"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"}
  ],
  "usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":80}}
}`)
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	result, err := provider.Complete(context.Background(), ChatRequest{
		Model:    "gpt-test",
		Messages: []Message{{Role: "system", Content: "system rules"}, {Role: "user", Content: "work"}},
		Tools: []ToolDefinition{{Type: "function", Function: ToolDefinitionFunc{
			Name: "read_file", Parameters: map[string]any{"type": "object"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage == nil || result.Usage.PromptCacheHitTokens != 80 || result.Usage.PromptCacheMissTokens != 20 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestResponsesStreamParsesTextToolsAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call-2","name":"search","arguments":"{\"query\":\"x\"}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":4}}}}`+"\n\n")
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	events, err := provider.Stream(context.Background(), ChatRequest{Model: "gpt-test", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var calls []ToolCall
	var usage *TokenUsage
	for event := range events {
		switch event.Type {
		case "delta":
			text.WriteString(event.Delta)
		case "tool_calls":
			calls = event.ToolCalls
		case "usage":
			usage = event.Usage
		}
	}
	if text.String() != "hello" || len(calls) != 1 || calls[0].ID != "call-2" {
		t.Fatalf("text=%q calls=%#v", text.String(), calls)
	}
	if usage == nil || usage.PromptCacheHitTokens != 4 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestResponsesCompleteSendsAgentRequestContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{
			"User-Agent":          "EchoCode/0.3.0",
			"originator":          "echocode",
			"session-id":          "project:session",
			"thread-id":           "project:session",
			"x-client-request-id": "project:session",
			"x-echocode-turn-id":  "turn-42",
			"x-codex-window-id":   "project:session:0",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}
		var request responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ToolChoice != "auto" || request.ParallelToolCalls || request.Store {
			t.Errorf("request controls = %#v", request)
		}
		if !reflect.DeepEqual(request.Include, []string{"reasoning.encrypted_content"}) {
			t.Errorf("include = %#v", request.Include)
		}
		if request.PromptCacheKey != "project:session" {
			t.Errorf("prompt_cache_key = %q", request.PromptCacheKey)
		}
		for key, want := range map[string]string{
			responsesInstallationMetadataKey: "11111111-2222-5333-8444-555555555555",
			"session_id":                     "project:session",
			"thread_id":                      "project:session",
			"turn_id":                        "turn-42",
			responsesWindowMetadataKey:       "project:session:0",
		} {
			if got := request.ClientMetadata[key]; got != want {
				t.Errorf("client_metadata[%s] = %q, want %q", key, got, want)
			}
		}
		turnMetadata := request.ClientMetadata[responsesTurnMetadataKey]
		if turnMetadata == "" || turnMetadata != r.Header.Get(responsesTurnMetadataKey) {
			t.Fatalf("turn metadata body/header mismatch: body=%q header=%q", turnMetadata, r.Header.Get(responsesTurnMetadataKey))
		}
		for _, value := range []byte(turnMetadata) {
			if value > 0x7f {
				t.Fatalf("turn metadata header contains non-ASCII byte: %q", turnMetadata)
			}
		}
		var snapshot map[string]any
		if err := json.Unmarshal([]byte(turnMetadata), &snapshot); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"installation_id": "11111111-2222-5333-8444-555555555555",
			"session_id":      "project:session",
			"thread_id":       "project:session",
			"turn_id":         "turn-42",
			"window_id":       "project:session:0",
			"request_kind":    "turn",
			"thread_source":   "user",
			"sandbox":         "workspace-write",
			"client_name":     "EchoCode",
			"client_version":  "0.3.0",
			"task_kind":       "chat",
		} {
			if got := snapshot[key]; got != want {
				t.Errorf("turn metadata[%s] = %#v, want %q", key, got, want)
			}
		}
		if got := int64(snapshot["turn_started_at_unix_ms"].(float64)); got != 1_784_736_031_000 {
			t.Errorf("turn_started_at_unix_ms = %d", got)
		}
		workspaces, ok := snapshot["workspaces"].(map[string]any)
		if !ok || workspaces[`C:\工作区`] == nil {
			t.Errorf("workspaces = %#v", snapshot["workspaces"])
		}
		if _, exists := request.ClientMetadata["client_name"]; exists {
			t.Error("custom metadata must be nested in the canonical turn snapshot")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-test","output":[]}`)
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{
		BaseURL: server.URL, APIKey: "test", APIType: "responses", ClientVersion: "0.3.0",
	}
	_, err := provider.Complete(context.Background(), ChatRequest{
		Model: "gpt-test", Messages: []Message{{Role: "user", Content: "work"}},
		Metadata:  map[string]string{"task_kind": "chat", "sandbox": "danger-full-access"},
		SessionID: "project:session", ThreadID: "project:session", TurnID: "turn-42",
		PromptCacheKey: "project:session",
		ResponsesContext: ResponsesClientContext{
			InstallationID:      "11111111-2222-5333-8444-555555555555",
			WindowID:            "project:session:0",
			RequestKind:         "turn",
			ThreadSource:        "user",
			Sandbox:             "workspace-write",
			WorkspaceRoots:      []string{`C:\工作区`},
			TurnStartedAtUnixMS: 1_784_736_031_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResponsesCompleteReportsServerModelReroute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("OpenAI-Model", "gpt-restricted")
		w.Header().Set("x-request-id", "req-reroute")
		_, _ = io.WriteString(w, `{"model":"gpt-requested","output":[]}`)
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	result, err := provider.Complete(context.Background(), ChatRequest{
		Model: "gpt-requested", Messages: []Message{{Role: "user", Content: "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveModel != "gpt-restricted" || len(result.Notices) != 1 {
		t.Fatalf("result = %#v", result)
	}
	notice := result.Notices[0]
	if notice.Kind != ProviderNoticeModelReroute || notice.RequestedModel != "gpt-requested" || notice.EffectiveModel != "gpt-restricted" || notice.RequestID != "req-reroute" {
		t.Fatalf("notice = %#v", notice)
	}
}

func TestResponsesStreamReportsSafetyAndProviderMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-codex-safety-buffering-enabled", "true")
		w.Header().Set("x-codex-safety-buffering-faster-model", "gpt-fast-header")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-requested\"},\"headers\":{\"OpenAI-Model\":\"gpt-restricted\"},\"safety_buffering\":false}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.metadata\",\"metadata\":{\"openai_verification_recommendation\":[\"trusted_access_for_cyber\",\"unknown_future_value\"],\"openai_chatgpt_moderation_metadata\":{\"presentation\":\"inline\",\"category\":\"cyber\"}}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\",\"safety_buffering\":{\"use_cases\":[\"cyber\"],\"reasons\":[\"policy-check\"]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-requested\",\"output\":[]}}\n\n")
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model: "gpt-requested", Messages: []Message{{Role: "user", Content: "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	notices := map[string]ProviderNotice{}
	var text strings.Builder
	for event := range events {
		if event.Type == "error" {
			t.Fatalf("stream error: %s", event.Error)
		}
		if event.Type == "delta" {
			text.WriteString(event.Delta)
		}
		if event.Notice != nil {
			notices[event.Notice.Kind] = *event.Notice
		}
	}
	if text.String() != "ok" {
		t.Fatalf("text = %q", text.String())
	}
	if got := notices[ProviderNoticeModelReroute].EffectiveModel; got != "gpt-restricted" {
		t.Fatalf("reroute = %#v", notices[ProviderNoticeModelReroute])
	}
	if got := notices[ProviderNoticeSafetyBuffering].RetryModel; got != "gpt-fast-header" {
		t.Fatalf("safety buffering = %#v", notices[ProviderNoticeSafetyBuffering])
	}
	if got := notices[ProviderNoticeModelVerification].Verifications; !reflect.DeepEqual(got, []string{"trusted_access_for_cyber"}) {
		t.Fatalf("verifications = %#v", got)
	}
	if got := notices[ProviderNoticeModeration].MetadataKeys; !reflect.DeepEqual(got, []string{"category", "presentation"}) {
		t.Fatalf("moderation keys = %#v", got)
	}
}

func TestResponsesSafetyBufferingRetryModelTriState(t *testing.T) {
	for name, testCase := range map[string]struct {
		payload string
		want    string
	}{
		"omitted uses header":  {`{"use_cases":["cyber"],"reasons":["risk"]}`, "gpt-fast-header"},
		"null disables header": {`{"use_cases":["cyber"],"reasons":["risk"],"retry_model":null}`, ""},
		"wire wins":            {`{"use_cases":["cyber"],"reasons":["risk"],"retry_model":"gpt-fast-wire"}`, "gpt-fast-wire"},
	} {
		t.Run(name, func(t *testing.T) {
			buffering := parseResponsesSafetyBuffering(json.RawMessage(testCase.payload), "gpt-fast-header")
			if buffering == nil || buffering.RetryModel != testCase.want {
				t.Fatalf("buffering = %#v, want retry model %q", buffering, testCase.want)
			}
		})
	}
	if buffering := parseResponsesSafetyBuffering(json.RawMessage(`false`), "gpt-fast-header"); buffering != nil {
		t.Fatalf("false safety_buffering = %#v", buffering)
	}
}

func TestResponsesCyberPolicyErrorIsTypedAndNotRetried(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req-policy")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"request blocked","type":"invalid_request","code":"cyber_policy"}}`)
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	_, err := provider.Complete(context.Background(), ChatRequest{
		Model: "gpt-requested", Messages: []Message{{Role: "user", Content: "work"}},
	})
	if err == nil {
		t.Fatal("expected cyber policy error")
	}
	info, ok := ProviderErrorDetails(err)
	if !ok || info.Code != "cyber_policy" || info.Type != "invalid_request" || info.HTTPStatus != http.StatusInternalServerError || info.RequestID != "req-policy" || info.Retryable {
		t.Fatalf("provider error = %#v, ok=%v, err=%v", info, ok, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestResponsesStreamCyberPolicyErrorPreservesTransportDetails(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req-stream-policy")
		_, _ = io.WriteString(w, `data: {"type":"response.failed","response":{"error":{"message":"request blocked","type":"invalid_request","code":"cyber_policy"}}}`+"\n\n")
	}))
	defer server.Close()

	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "test", APIType: "responses"}
	events, err := provider.Stream(context.Background(), ChatRequest{
		Model: "gpt-requested", Messages: []Message{{Role: "user", Content: "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var providerError *ProviderErrorInfo
	for event := range events {
		if event.Type == "error" {
			providerError = event.ProviderError
		}
	}
	if providerError == nil || providerError.Code != "cyber_policy" || providerError.Type != "invalid_request" ||
		providerError.HTTPStatus != http.StatusOK || providerError.RequestID != "req-stream-policy" || providerError.Retryable {
		t.Fatalf("provider error = %#v", providerError)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}
