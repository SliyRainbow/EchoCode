package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPublicMCPAndSkillConfigurationReachesAgentToolLoop(t *testing.T) {
	remote := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "release-extension", Version: "1.0.0"}, nil)
	var calls atomic.Int32
	sdkmcp.AddTool(remote, &sdkmcp.Tool{Name: "echo", Description: "Read the fixture marker", Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			calls.Add(1)
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "MCP_EXTENSION_OK"}}}, nil, nil
		})
	mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return remote }, &sdkmcp.StreamableHTTPOptions{JSONResponse: true}))
	defer mcpServer.Close()
	var modelRequests atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(value string) { _, _ = fmt.Fprintf(w, "data: %s\n\n", value) }
		if modelRequests.Add(1) == 1 {
			if !strings.Contains(string(body), "mcp__release-extension__echo") || !strings.Contains(string(body), "load_skill") {
				t.Error("configured extensions were absent from model tools")
			}
			emit(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"remote-1","type":"function","function":{"name":"mcp__release-extension__echo","arguments":"{}"}},{"index":1,"id":"skill-1","type":"function","function":{"name":"load_skill","arguments":"{\"name\":\"release-fixture\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			if !strings.Contains(string(body), "MCP_EXTENSION_OK") || !strings.Contains(string(body), "SKILL_EXTENSION_OK") {
				t.Error("extension results were not returned to the model")
			}
			emit(`{"choices":[{"delta":{"content":"EXTENSIONS_OK"},"finish_reason":"stop"}]}`)
		}
		emit("[DONE]")
	}))
	defer modelServer.Close()
	root := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), UserSkillsDir: filepath.Join(root, "skills"), SettingsPath: filepath.Join(root, "settings.json"), SessionsDir: filepath.Join(root, "sessions")})
	defer service.Close()
	service.SetApprovalNotify(func(request ApprovalRequest) {
		go func() {
			_ = service.RespondApproval(request.ID, request.Tool, request.Tool == "mcp__release-extension__echo", "once")
		}()
	})
	settings := service.WorkbenchState().RuntimeSettings
	settings.WorkspaceRoot = t.TempDir()
	settings.FilesystemAccess, settings.SandboxMode = "read-only", "read-only"
	settings.ShellAccess = false
	settings.MCP.Servers = []MCPServerSetting{{ID: "release-extension", Name: "Fixture", Transport: string(mcp.TransportStreamableHTTP), URL: mcpServer.URL, Enabled: true}}
	settings.Model = ModelSettings{SelectedProviderID: "local-fixture", SelectedModelID: "test-model", Providers: []ModelProviderSetting{{ID: "local-fixture", Enabled: true, Protocol: "local", APIType: "chat-completions", BaseURL: modelServer.URL, DefaultModelID: "test-model", Models: []ProviderModel{{ID: "test-model", Provider: "local-fixture"}}}}}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportSkillMarkdown("release-fixture.md", "# Fixture\nSKILL_EXTENSION_OK\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	state := service.ConfigureMCP(ctx, "")
	if len(state.MCPServers) != 1 || state.MCPServers[0].State != "ready" {
		t.Fatalf("MCP configuration failed: %#v", state.MCPServers)
	}
	result, err := service.SendChatMessage(ctx, "Run the configured MCP and Skill fixture tools.")
	if err != nil || result.Content != "EXTENSIONS_OK" || !result.TurnCommitted || calls.Load() != 1 {
		t.Fatalf("extension loop failed: content=%q committed=%t calls=%d err=%v", result.Content, result.TurnCommitted, calls.Load(), err)
	}
	for _, name := range []string{"mcp__release-extension__echo", "load_skill"} {
		requireLiveTool(t, result, name)
	}
	settings.MCP.Servers[0].Enabled = false
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	service.ConfigureMCP(ctx, "")
	for _, definition := range service.buildToolRegistry().Schemas() {
		if definition.Function.Name == "mcp__release-extension__echo" {
			t.Fatal("disabled MCP tool remained registered")
		}
	}
	// Ensure snapshots remain JSON serializable for the desktop boundary.
	if _, err := json.Marshal(service.WorkbenchState()); err != nil {
		t.Fatal(err)
	}
}
