package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestAgentRoundTripsToolsAcrossProtocols(t *testing.T) {
	for _, dialect := range []string{"chat", "responses", "messages"} {
		t.Run(dialect, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "roundtrip.txt"), []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var wire map[string]any
				if err := json.Unmarshal(body, &wire); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				if len(wire["tools"].([]any)) == 0 {
					t.Error("Agent omitted its tools")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(value string) { _, _ = fmt.Fprintf(w, "data: %s\n\n", value) }
				if requests.Add(1) == 1 {
					switch dialect {
					case "chat":
						emit(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-list","type":"function","function":{"name":"list_dir","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
						emit(`[DONE]`)
					case "responses":
						if _, bad := wire["client_metadata"]; bad {
							t.Error("private body field leaked")
						}
						emit(`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"Inspect files"}`)
						emit(`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"Inspect files"}`)
						emit(`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs-private","content":[{"type":"reasoning_text","text":"Inspect files"}],"summary":[],"encrypted_content":"opaque-private"}}`)
						emit(`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Listing files."}]}}`)
						emit(`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc-list","call_id":"call-list","name":"list_dir","arguments":"{}"}}`)
						emit(`{"type":"response.completed","response":{"status":"completed","output":[]}}`)
					case "messages":
						emit(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"Inspect files","signature":"opaque-private"}}`)
						emit(`{"type":"content_block_stop","index":0}`)
						emit(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-list","name":"list_dir","input":{}}}`)
						emit(`{"type":"content_block_stop","index":1}`)
						emit(`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
						emit(`{"type":"message_stop"}`)
					}
					return
				}
				if !strings.Contains(string(body), "roundtrip.txt") || !strings.Contains(string(body), "call-list") {
					t.Error("tool feedback or identity disappeared")
				}
				if dialect != "chat" && !strings.Contains(string(body), "opaque-private") {
					t.Error("opaque reasoning continuation disappeared")
				}
				switch dialect {
				case "chat":
					emit(`{"choices":[{"delta":{"content":"ROUNDTRIP_OK"},"finish_reason":"stop"}]}`)
					emit(`[DONE]`)
				case "responses":
					if !strings.Contains(string(body), `"phase":"commentary"`) {
						t.Error("assistant phase disappeared")
					}
					emit(`{"type":"response.output_text.delta","delta":"ROUNDTRIP_OK"}`)
					emit(`{"type":"response.completed","response":{"status":"completed","output":[]}}`)
				case "messages":
					messages := wire["messages"].([]any)
					last := messages[len(messages)-1].(map[string]any)
					if last["role"] != "user" || last["content"].([]any)[0].(map[string]any)["type"] != "tool_result" {
						t.Error("native tool result was not the first user block")
					}
					emit(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ROUNDTRIP_OK"}}`)
					emit(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
					emit(`{"type":"message_stop"}`)
				}
			}))
			defer server.Close()
			memory := vault.NewMemoryVault()
			_ = memory.Set("EchoCode", "model-provider:roundtrip:api-key", "test-key")
			service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir(), Vault: memory})
			defer service.Close()
			service.runtimeSettings.WorkspaceRoot = workspace
			service.runtimeSettings.SandboxMode = "read-only"
			service.runtimeSettings.FilesystemAccess = "read-only"
			service.runtimeSettings.NetworkAccess = false
			service.runtimeSettings.ShellAccess = false
			protocolName, apiType := "openai-compatible", "chat-completions"
			if dialect == "responses" {
				apiType = "responses"
			}
			if dialect == "messages" {
				protocolName = "anthropic-compatible"
				apiType = "anthropic-messages"
			}
			service.runtimeSettings.Model = ModelSettings{SelectedProviderID: "roundtrip", SelectedModelID: "test-model", Providers: []ModelProviderSetting{{ID: "roundtrip", Name: "Roundtrip", Enabled: true, Protocol: protocolName, APIType: apiType, BaseURL: server.URL, DefaultModelID: "test-model", Models: []ProviderModel{{ID: "test-model", Provider: "roundtrip"}}}}}
			result, err := service.SendChatMessage(context.Background(), "List the files in the current workspace.")
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 2 || result.Content != "ROUNDTRIP_OK" || !result.TurnCommitted {
				t.Fatalf("incomplete round trip: %#v, requests=%d", result, requests.Load())
			}
			if dialect != "chat" && result.Reasoning != "Inspect files" {
				t.Fatalf("visible reasoning missing: %q", result.Reasoning)
			}
			if strings.Contains(result.Content, result.Reasoning) && result.Reasoning != "" {
				t.Fatal("reasoning mixed into the final answer")
			}
		})
	}
}
