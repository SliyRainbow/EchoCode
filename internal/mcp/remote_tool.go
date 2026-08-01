package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type RemoteTool struct {
	manager     *Manager
	serverID    string
	remoteName  string
	name        string
	description string
	schema      map[string]any
	readOnly    bool
}

func (t *RemoteTool) Name() string { return t.name }

func (t *RemoteTool) Description() string {
	if t.description == "" {
		return fmt.Sprintf("MCP tool %s from server %s", t.remoteName, t.serverID)
	}
	return fmt.Sprintf("[%s] %s", t.serverID, t.description)
}

func (t *RemoteTool) InputSchema() map[string]any { return t.schema }

func (t *RemoteTool) ReadOnly() bool { return t.readOnly }

func (t *RemoteTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.manager == nil {
		return tools.Result{}, errors.New("MCP manager 不可用")
	}
	var arguments map[string]any
	if len(rawArgs) == 0 {
		arguments = map[string]any{}
	} else if err := json.Unmarshal(rawArgs, &arguments); err != nil {
		return tools.Result{}, fmt.Errorf("MCP 工具参数不是有效 JSON 对象: %w", err)
	}
	return t.manager.callTool(ctx, t.serverID, t.remoteName, t.name, arguments, rawArgs)
}

func (m *Manager) callTool(ctx context.Context, serverID, remoteName, displayName string, arguments map[string]any, rawArgs json.RawMessage) (tools.Result, error) {
	m.mu.RLock()
	server := m.servers[serverID]
	if server == nil || server.session == nil || server.status.State != "ready" {
		m.mu.RUnlock()
		return tools.Result{}, fmt.Errorf("MCP 服务器 %s 未连接，请刷新服务器后重试", serverID)
	}
	session := server.session
	resultPolicy := server.config.ToolResultPolicy
	m.mu.RUnlock()
	tools.EmitProgress(ctx, tools.ResultPart{
		Kind:   tools.PartToolCall,
		Name:   displayName,
		Status: "waiting",
		Input:  truncateText(string(rawArgs), 1200),
		Output: fmt.Sprintf("正在等待 MCP 服务器 %s 返回结果", serverID),
	})
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: remoteName, Arguments: arguments})
	if err != nil {
		m.mu.Lock()
		if m.servers[serverID] == server {
			server.status.Message = truncateText(err.Error(), 1200)
		}
		m.mu.Unlock()
		return tools.Result{}, err
	}
	summary := summarizeCallToolResult(result, resultPolicy)
	status := "ok"
	if result.IsError {
		status = "error"
	}
	return tools.Result{
		Summary: summary,
		IsError: result.IsError,
		Parts: []tools.ResultPart{{
			Kind:   tools.PartToolCall,
			Name:   displayName,
			Status: status,
			Input:  truncateText(string(rawArgs), 1200),
			Output: summary,
		}},
	}, nil
}

func summarizeCallToolResult(result *sdkmcp.CallToolResult, policy string) string {
	if result == nil {
		return "MCP 工具未返回内容"
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, content := range result.Content {
		switch item := content.(type) {
		case *sdkmcp.TextContent:
			if text := strings.TrimSpace(item.Text); text != "" {
				parts = append(parts, text)
			}
		case *sdkmcp.ResourceLink:
			parts = append(parts, fmt.Sprintf("resource: %s", item.URI))
		case *sdkmcp.ImageContent:
			parts = append(parts, fmt.Sprintf("image: %s (%d bytes)", item.MIMEType, len(item.Data)))
		case *sdkmcp.AudioContent:
			parts = append(parts, fmt.Sprintf("audio: %s (%d bytes)", item.MIMEType, len(item.Data)))
		default:
			if encoded, err := json.Marshal(item); err == nil {
				parts = append(parts, string(encoded))
			}
		}
	}
	if result.StructuredContent != nil {
		if encoded, err := json.Marshal(result.StructuredContent); err == nil {
			structured := string(encoded)
			if len(parts) == 0 || !strings.Contains(strings.Join(parts, "\n"), structured) {
				parts = append(parts, structured)
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "MCP 工具执行完成，无文本输出")
	}
	limit := 12 * 1024
	switch policy {
	case "balanced":
		limit = 32 * 1024
	case "raw-local":
		limit = 64 * 1024
	}
	return truncateText(strings.Join(parts, "\n"), limit)
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("\n... [truncated %d bytes]", len(value)-limit)
}
