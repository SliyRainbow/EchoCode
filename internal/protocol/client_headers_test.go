package protocol

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAICompatibleClientHeadersReachEveryEndpoint(t *testing.T) {
	for _, custom := range []bool{false, true} {
		for _, operation := range []string{"models", "chat-complete", "chat-stream", "responses-complete", "responses-stream"} {
			t.Run(fmt.Sprintf("%s/custom=%t", operation, custom), func(t *testing.T) {
				isResponses := strings.HasPrefix(operation, "responses-")
				isStream := strings.HasSuffix(operation, "-stream")
				path := "/chat/completions"
				if operation == "models" {
					path = "/models"
				}
				if isResponses {
					path = "/responses"
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					wantUA, wantOriginator := "EchoCode/0.3.20", "echocode"
					if custom {
						wantUA, wantOriginator = "RelayCompatible/1.0", "custom-client"
					}
					if r.URL.Path != path {
						t.Errorf("path = %q, want %q", r.URL.Path, path)
					}
					for name, want := range map[string]string{"User-Agent": wantUA, "originator": wantOriginator, "Authorization": "Bearer own-test-key"} {
						if got := r.Header.Get(name); got != want {
							t.Errorf("%s = %q, want %q", name, got, want)
						}
					}
					if custom && r.Header.Get("X-Gateway-Tag") != "test-tag" {
						t.Error("custom header did not reach the server")
					}
					if isResponses {
						for name, want := range map[string]string{"session-id": "session-current", "thread-id": "thread-current", "x-echocode-turn-id": "turn-current"} {
							if got := r.Header.Get(name); got != want {
								t.Errorf("generated %s = %q, want %q", name, got, want)
							}
						}
					}
					w.Header().Set("Content-Type", "application/json")
					switch {
					case operation == "models":
						_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
					case isResponses && isStream:
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
					case isResponses:
						_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
					case isStream:
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
					default:
						_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
					}
				}))
				defer server.Close()
				provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "own-test-key", ClientVersion: "0.3.20"}
				if isResponses {
					provider.APIType = "responses"
				}
				if custom {
					provider.ExtraHeaders = "user-agent: RelayCompatible/1.0\r\nORIGINATOR: custom-client\r\nX-Gateway-Tag: test-tag\r\nsession-id: stale-static-session"
				}
				ctx := context.Background()
				request := ChatRequest{Model: "test-model", Messages: []Message{{Role: "user", Content: "ping"}}, SessionID: "session-current", ThreadID: "thread-current", TurnID: "turn-current"}
				switch {
				case operation == "models":
					models, err := provider.ListModels(ctx)
					if err != nil || len(models) != 1 {
						t.Fatalf("model discovery = %#v, %v", models, err)
					}
				case isStream:
					events, err := provider.Stream(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
					var answer strings.Builder
					for event := range events {
						if event.Type == "error" {
							t.Errorf("stream failed: %s", event.Error)
						}
						if event.Type == "delta" {
							answer.WriteString(event.Delta)
						}
					}
					if answer.String() != "ok" {
						t.Fatalf("stream output = %q", answer.String())
					}
				default:
					result, err := provider.Complete(ctx, request)
					if err != nil || result.Content != "ok" {
						t.Fatalf("completion = %#v, %v", result, err)
					}
				}
				if calls.Load() != 1 {
					t.Fatalf("requests = %d, want 1", calls.Load())
				}
			})
		}
	}
}

func TestOpenAICompatibleRetriesKeepCustomClientHeaders(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "RelayCompatible/1.0" || r.Header.Get("originator") != "custom-client" {
			t.Error("retry lost explicit client headers")
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
	}))
	defer server.Close()
	provider := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "own-test-key", ExtraHeaders: "User-Agent: RelayCompatible/1.0\noriginator: custom-client"}
	if _, err := provider.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("requests = %d, want 2", calls.Load())
	}
}
