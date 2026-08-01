package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalmcp "github.com/SliyRainbow/EchoCode/internal/mcp"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentMCPAddInput struct {
	A int `json:"a" jsonschema:"first number"`
	B int `json:"b" jsonschema:"second number"`
}

func TestAgentMCPToolLoopPreservesPrivateContext(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "agent-mcp-test", Version: "1.0.0"}, nil)
	var remoteCalls atomic.Int32
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "add",
		Description: "Add two integers",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, input agentMCPAddInput) (*sdkmcp.CallToolResult, any, error) {
		remoteCalls.Add(1)
		sum := input.A + input.B
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "sum=" + strconv.Itoa(sum)},
		}}, map[string]int{"sum": sum}, nil
	})
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "mutate",
		Description: "A mutating fixture tool",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, input agentMCPAddInput) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "mutated"},
		}}, map[string]bool{"mutated": true}, nil
	})

	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, &sdkmcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()

	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.NetworkAccess = true
	service.runtimeSettings.ApprovalPolicy = "never"
	service.projectMemory = ProjectMemoryState{Summary: "Remember the MCP integration fixture."}
	configureCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	statuses := service.mcpManager.Configure(configureCtx, []internalmcp.ServerConfig{{
		ID:               "agent-test",
		Name:             "Agent Test",
		Transport:        internalmcp.TransportStreamableHTTP,
		URL:              httpServer.URL,
		Enabled:          true,
		AllowNetwork:     true,
		ToolResultPolicy: "summary-first",
	}})
	if len(statuses) != 1 || statuses[0].State != "ready" {
		t.Fatalf("MCP statuses = %#v", statuses)
	}

	const addToolName = "mcp__agent-test__add"
	const mutateToolName = "mcp__agent-test__mutate"
	if _, ok := service.buildReadOnlyRegistry().Get(addToolName); !ok {
		t.Fatal("read-only MCP tool is missing from the Plan/explore registry")
	}
	if _, ok := service.buildReadOnlyRegistry().Get(mutateToolName); ok {
		t.Fatal("mutating MCP tool leaked into the read-only registry")
	}
	scopedCtx := withTurnTaskScope(context.Background(), turnTaskScope{
		Enabled: true,
		Roots:   []string{t.TempDir()},
	})
	scopedRegistry := service.buildToolRegistryForContext(scopedCtx)
	if _, ok := scopedRegistry.Get(addToolName); !ok {
		t.Fatal("read-only MCP tool should remain available in a scoped turn")
	}
	if _, ok := scopedRegistry.Get(mutateToolName); ok {
		t.Fatal("mutating MCP tool without a host-enforceable path boundary leaked into a scoped turn")
	}

	userPrompt := "Use the remote MCP tool to add 2 and 3."
	preview := service.contextPreviewForInput(userPrompt)
	stablePrompt := formatStablePrompt(preview)
	if !strings.Contains(stablePrompt, addToolName) {
		t.Fatalf("stable MCP snapshot is missing %q: %q", addToolName, stablePrompt)
	}
	messages := appendTurnRequestMessages([]protocol.Message{{Role: "system", Content: stablePrompt}}, preview, userPrompt, nil)
	privateContext := ""
	for _, message := range messages {
		if message.InternalKind == contextRequestKind {
			privateContext = message.Content
			break
		}
	}
	if !strings.Contains(privateContext, "Remember the MCP integration fixture") || !strings.Contains(privateContext, "output_requirements") {
		t.Fatalf("private turn context is incomplete: %q", privateContext)
	}

	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), service.buildToolRegistry(), protocol.ChatRequest{
		Model: "mcp-model", Messages: messages,
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if !requestContainsPrivateContext(request.Messages, privateContext) {
			t.Fatalf("completion %d lost the private turn context: %#v", completionCalls, request.Messages)
		}
		switch completionCalls {
		case 1:
			if !requestContainsTool(request, addToolName) || !requestContainsTool(request, mutateToolName) {
				t.Fatalf("main Agent request is missing MCP tools: %#v", request.Tools)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "mcp-add-1", Type: "function",
				Function: protocol.ToolCallFunction{Name: addToolName, Arguments: json.RawMessage(`{"a":2,"b":3}`)},
			}}}, nil
		case 2:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || last.Name != addToolName || !strings.Contains(last.Content, "sum=5") {
				t.Fatalf("MCP result was not fed back to the model: %#v", last)
			}
			return protocol.CompletionResult{Content: "The MCP result is 5."}, nil
		default:
			t.Fatalf("unexpected completion call %d", completionCalls)
			return protocol.CompletionResult{}, nil
		}
	}, nil)
	if err != nil || outcome.Content != "The MCP result is 5." || completionCalls != 2 || remoteCalls.Load() != 1 {
		t.Fatalf("outcome=%#v completionCalls=%d remoteCalls=%d err=%v", outcome, completionCalls, remoteCalls.Load(), err)
	}
}

func requestContainsPrivateContext(messages []protocol.Message, expected string) bool {
	for _, message := range messages {
		if message.InternalKind == contextRequestKind && message.Content == expected {
			return true
		}
	}
	return false
}

func requestContainsTool(request protocol.ChatRequest, name string) bool {
	for _, definition := range request.Tools {
		if definition.Function.Name == name {
			return true
		}
	}
	return false
}
