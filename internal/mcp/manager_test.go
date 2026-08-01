package mcp

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
