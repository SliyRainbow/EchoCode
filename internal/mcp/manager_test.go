package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type addInput struct {
	A int `json:"a" jsonschema:"first number"`
	B int `json:"b" jsonschema:"second number"`
}

func TestManagerStreamableHTTPDiscoverAndExecute(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1.2.3"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "add",
		Description: "Add two integers",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, input addInput) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "sum=" + strconv.Itoa(input.A+input.B)},
		}}, map[string]int{"sum": input.A + input.B}, nil
	})

	var sawAuth atomic.Bool
	handler := sdkmcp.NewStreamableHTTPHandler(func(request *http.Request) *sdkmcp.Server {
		if request.Header.Get("Authorization") == "Bearer integration-test" {
			sawAuth.Store(true)
		}
		return server
	}, &sdkmcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	manager := NewManager()
	defer manager.Close()
	config := ServerConfig{
		ID:               "test-server",
		Name:             "Test Server",
		Transport:        TransportStreamableHTTP,
		URL:              httpServer.URL,
		Headers:          []KeyValue{{Key: "Authorization", Value: "Bearer integration-test"}},
		Enabled:          true,
		AllowNetwork:     true,
		ToolResultPolicy: "summary-first",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statuses := manager.Configure(ctx, []ServerConfig{config})
	if len(statuses) != 1 || statuses[0].State != "ready" {
		t.Fatalf("statuses = %#v, want one ready server", statuses)
	}
	if statuses[0].ToolCount != 1 || statuses[0].ServerVersion != "1.2.3" {
		t.Fatalf("status metadata = %#v", statuses[0])
	}
	if !sawAuth.Load() {
		t.Fatal("configured HTTP Authorization header was not sent")
	}

	available := manager.Tools()
	if len(available) != 1 || available[0].Name() != "mcp__test-server__add" {
		t.Fatalf("tools = %#v", available)
	}
	remote, ok := available[0].(*RemoteTool)
	if !ok || !remote.ReadOnly() {
		t.Fatalf("tool = %#v, want read-only RemoteTool", available[0])
	}
	progress := make([]tools.ResultPart, 0, 1)
	callCtx := tools.WithProgressSink(ctx, func(part tools.ResultPart) {
		progress = append(progress, part)
	})
	result, err := available[0].Execute(callCtx, json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Summary, "sum=5") || !strings.Contains(result.Summary, `"sum":5`) {
		t.Fatalf("result = %#v", result)
	}
	if len(progress) != 1 || progress[0].Status != "waiting" || !strings.Contains(progress[0].Output, "test-server") {
		t.Fatalf("MCP progress = %#v", progress)
	}

	snapshots := manager.Snapshots()
	if len(snapshots) != 1 || len(snapshots[0].Tools) != 1 || snapshots[0].Tools[0].Name != "mcp__test-server__add" {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestToolsForWorkspaceInjectsCodeGraphProjectPathPerRuntime(t *testing.T) {
	config := ServerConfig{
		ID:        "codegraph",
		Name:      "CodeGraph",
		Transport: TransportStdio,
		Command:   `C:\Users\tester\AppData\Roaming\npm\codegraph.cmd`,
		Args:      []string{"serve", "--mcp"},
		Enabled:   true,
	}
	manager := managerWithRemoteTool(config, &sdkmcp.Tool{
		Name:        "codegraph_explore",
		Description: "Explore code relationships",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string"},
				"projectPath": map[string]any{"type": "string"},
			},
		},
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	})

	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	first := onlyRemoteTool(t, manager.ToolsForWorkspace(firstRoot))
	second := onlyRemoteTool(t, manager.ToolsForWorkspace(secondRoot))

	firstArgs, firstRaw, firstWorkingDirectory, err := first.argumentsForCall(json.RawMessage(`{"query":"session runtime"}`))
	if err != nil {
		t.Fatal(err)
	}
	secondArgs, _, secondWorkingDirectory, err := second.argumentsForCall(json.RawMessage(`{"query":"background runtime"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := firstArgs["projectPath"], canonicalWorkspaceRoot(firstRoot); got != want {
		t.Fatalf("first projectPath = %#v, want %q", got, want)
	}
	if got, want := secondArgs["projectPath"], canonicalWorkspaceRoot(secondRoot); got != want {
		t.Fatalf("second projectPath = %#v, want %q", got, want)
	}
	if firstWorkingDirectory != canonicalWorkspaceRoot(firstRoot) || secondWorkingDirectory != canonicalWorkspaceRoot(secondRoot) {
		t.Fatalf("working directories = %q, %q", firstWorkingDirectory, secondWorkingDirectory)
	}
	if !strings.Contains(string(firstRaw), `"projectPath"`) {
		t.Fatalf("effective args = %s, want injected projectPath", firstRaw)
	}
	if !first.ReadOnly() || !second.ReadOnly() {
		t.Fatal("CodeGraph tool should retain its read-only annotation")
	}
}

func TestCodeGraphWorkspaceInjectionPreservesExplicitProjectPath(t *testing.T) {
	tool := &RemoteTool{workspaceRoot: canonicalWorkspaceRoot(t.TempDir()), injectProjectPath: true}
	explicit := filepath.Join(t.TempDir(), "explicit")
	arguments, _, workingDirectory, err := tool.argumentsForCall(json.RawMessage(fmt.Sprintf(`{"query":"impact","projectPath":%q}`, explicit)))
	if err != nil {
		t.Fatal(err)
	}
	if got := arguments["projectPath"]; got != explicit {
		t.Fatalf("projectPath = %#v, want explicit %q", got, explicit)
	}
	if workingDirectory != explicit {
		t.Fatalf("working directory = %q, want %q", workingDirectory, explicit)
	}
}

func TestWorkspacePathIsNotInjectedIntoOtherMCPServers(t *testing.T) {
	configs := []ServerConfig{
		{ID: "other", Transport: TransportStdio, Command: "other-mcp", Args: []string{"serve", "--mcp"}, Enabled: true},
		{ID: "remote-codegraph", Transport: TransportStreamableHTTP, Command: "codegraph", Args: []string{"serve", "--mcp"}, Enabled: true},
	}
	for _, config := range configs {
		t.Run(config.ID, func(t *testing.T) {
			manager := managerWithRemoteTool(config, &sdkmcp.Tool{
				Name: "query",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"query":       map[string]any{"type": "string"},
					"projectPath": map[string]any{"type": "string"},
				}},
			})
			remote := onlyRemoteTool(t, manager.ToolsForWorkspace(t.TempDir()))
			arguments, _, workingDirectory, err := remote.argumentsForCall(json.RawMessage(`{"query":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := arguments["projectPath"]; exists {
				t.Fatalf("non-local CodeGraph MCP received projectPath: %#v", arguments)
			}
			if workingDirectory != "" {
				t.Fatalf("working directory = %q, want empty", workingDirectory)
			}
		})
	}
}

func TestCodeGraphToolWithoutProjectPathSchemaIsNotModified(t *testing.T) {
	config := ServerConfig{ID: "codegraph", Transport: TransportStdio, Command: "codegraph", Args: []string{"serve", "--mcp"}, Enabled: true}
	manager := managerWithRemoteTool(config, &sdkmcp.Tool{
		Name:        "legacy_tool",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
	})
	remote := onlyRemoteTool(t, manager.ToolsForWorkspace(t.TempDir()))
	arguments, _, _, err := remote.argumentsForCall(json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := arguments["projectPath"]; exists {
		t.Fatalf("tool without projectPath schema was modified: %#v", arguments)
	}
}

func managerWithRemoteTool(config ServerConfig, remote *sdkmcp.Tool) *Manager {
	return &Manager{
		servers: map[string]*managedServer{
			config.ID: {
				config:  config,
				session: &sdkmcp.ClientSession{},
				tools:   map[string]*sdkmcp.Tool{remote.Name: remote},
				status:  ServerStatus{State: "ready"},
			},
		},
		order: []string{config.ID},
	}
}

func onlyRemoteTool(t *testing.T, available []tools.Tool) *RemoteTool {
	t.Helper()
	if len(available) != 1 {
		t.Fatalf("tools = %#v, want one", available)
	}
	remote, ok := available[0].(*RemoteTool)
	if !ok {
		t.Fatalf("tool = %T, want *RemoteTool", available[0])
	}
	return remote
}
