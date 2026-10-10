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

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestAllToolRegistriesExportRequiredArrays(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.NetworkAccess = true
	service.runtimeSettings.ShellAccess = true
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	for name, registry := range map[string]*tools.Registry{
		"main": service.buildToolRegistry(), "worker": service.buildWorkerToolRegistry(), "plan": service.buildReadOnlyRegistry(),
	} {
		// Native-only tools must obey the same wire contract even when their
		// host bridges are unavailable in this test process.
		registry.Add(tools.BrowserTool{})
		registry.Add(tools.ComputerTool{})
		registry.Add(tools.RenderArtifactTool{})
		registry.Add(tools.InspectVisualTool{})
		registry.Add(tools.OpenFileTool{})
		registry.Add(GitTool{})
		registry.Add(TerminalTool{})
		encoded, err := json.Marshal(toolDefinitions(registry))
		if err != nil {
			t.Fatal(err)
		}
		var definitions []protocol.ToolDefinition
		if err := json.Unmarshal(encoded, &definitions); err != nil {
			t.Fatal(err)
		}
		for _, definition := range definitions {
			if _, ok := definition.Function.Parameters["required"].([]any); !ok {
				t.Errorf("%s tool %s has no required array: %s", name, definition.Function.Name, encoded)
			}
		}
	}
}

func TestChatToolLoopPassesGatewaySchemaValidation(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "schema-check.txt"), []byte("test fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Tools    []protocol.ToolDefinition `json:"tools"`
			Messages []protocol.Message        `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		if len(request.Tools) == 0 {
			t.Error("real Agent request did not advertise tools")
			http.Error(w, "missing tools", 400)
			return
		}
		for _, definition := range request.Tools {
			// Reproduce gateways that deserialize omitted required fields to null.
			if _, ok := definition.Function.Parameters["required"].([]any); !ok {
				http.Error(w, fmt.Sprintf(`{"error":{"message":"Invalid schema for function '%s': null is not of type array"}}`, definition.Function.Name), 400)
				return
			}
		}
		if requests.Add(1) == 1 {
			if requestIsStream(body) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"list-call\",\"type\":\"function\",\"function\":{\"name\":\"list_dir\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			} else {
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"list-call","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}]}`)
			}
			return
		}
		last := request.Messages[len(request.Messages)-1]
		for _, message := range request.Messages {
			for _, call := range message.ToolCalls {
				var arguments string
				if err := json.Unmarshal(call.Function.Arguments, &arguments); err != nil {
					t.Errorf("tool arguments must be a wire string: %s", call.Function.Arguments)
					http.Error(w, "tool arguments must be a string", 400)
					return
				}
			}
		}
		if last.Role != "tool" || last.ToolCallID != "list-call" || !strings.Contains(last.Content, "schema-check.txt") {
			t.Errorf("tool feedback omitted directory entries: %#v", last)
		}
		writeOpenAIReply(w, requestIsStream(body), "schema-check.txt", "")
	}))
	defer server.Close()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "read-only"
	service.runtimeSettings.NetworkAccess = false
	service.runtimeSettings.ShellAccess = false
	service.runtimeSettings.Model = ModelSettings{SelectedProviderID: "local-schema", SelectedModelID: "test-model", Providers: []ModelProviderSetting{{ID: "local-schema", Name: "Local schema check", Enabled: true, Protocol: "local", APIType: "chat-completions", BaseURL: server.URL, DefaultModelID: "test-model", Models: []ProviderModel{{ID: "test-model"}}}}}
	result, err := service.SendChatMessage(context.Background(), "List the files in the current directory.")
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || result.Content != "schema-check.txt" || !result.TurnCommitted {
		t.Fatalf("tool cycle = %#v, requests=%d", result, requests.Load())
	}
	found := false
	for _, part := range result.Parts {
		if part.Name == "list_dir" && part.Status == "ok" && strings.Contains(part.Output, "schema-check.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("real directory tool result missing from output: %#v", result.Parts)
	}
}
