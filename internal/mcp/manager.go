package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	TransportBuiltin        = "builtin"
	TransportStdio          = "stdio"
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
)

type KeyValue struct {
	Key   string
	Value string
}

type ServerConfig struct {
	ID               string
	Name             string
	Transport        string
	Command          string
	Args             []string
	Env              []KeyValue
	PassEnvironment  []string
	WorkingDirectory string
	URL              string
	Headers          []KeyValue
	Enabled          bool
	ToolResultPolicy string
	WorkspaceRoot    string
	AllowNetwork     bool
}

type ServerStatus struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Transport       string `json:"transport"`
	State           string `json:"state"`
	Message         string `json:"message"`
	ToolCount       int    `json:"toolCount"`
	ProtocolVersion string `json:"protocolVersion,omitempty"`
	ServerVersion   string `json:"serverVersion,omitempty"`
	CheckedAt       string `json:"checkedAt,omitempty"`
}

type managedServer struct {
	config   ServerConfig
	key      string
	client   *sdkmcp.Client
	session  *sdkmcp.ClientSession
	tools    map[string]*sdkmcp.Tool
	snapshot ServerSnapshot
	status   ServerStatus
	stderr   *tailBuffer
}

type Manager struct {
	mu      sync.RWMutex
	servers map[string]*managedServer
	order   []string
}

func NewManager() *Manager {
	return &Manager{servers: map[string]*managedServer{}}
}

func (m *Manager) Configure(ctx context.Context, configs []ServerConfig) []ServerStatus {
	return m.configure(ctx, configs, "")
}

func (m *Manager) Refresh(ctx context.Context, configs []ServerConfig, serverID string) []ServerStatus {
	return m.configure(ctx, configs, strings.TrimSpace(serverID))
}

func (m *Manager) configure(ctx context.Context, configs []ServerConfig, forceID string) []ServerStatus {
	configs = normalizeServerConfigs(configs)
	m.mu.RLock()
	previous := make(map[string]*managedServer, len(m.servers))
	for id, server := range m.servers {
		previous[id] = server
	}
	m.mu.RUnlock()

	next := make(map[string]*managedServer, len(configs))
	order := make([]string, 0, len(configs))
	reused := map[*managedServer]bool{}
	for _, config := range configs {
		order = append(order, config.ID)
		key := serverConfigKey(config)
		if old := previous[config.ID]; old != nil && forceID != config.ID && old.key == key && old.session != nil && old.status.State == "ready" {
			next[config.ID] = old
			reused[old] = true
			continue
		}
		next[config.ID] = m.connectServer(ctx, config, key)
	}

	m.mu.Lock()
	m.servers = next
	m.order = order
	m.mu.Unlock()
	for _, server := range next {
		if server != nil && server.session != nil && !reused[server] {
			go m.watchServer(server)
		}
	}

	for _, old := range previous {
		if !reused[old] {
			closeManagedServer(old)
		}
	}
	return m.Statuses(configs)
}

func (m *Manager) connectServer(ctx context.Context, config ServerConfig, key string) *managedServer {
	status := ServerStatus{
		ID:        config.ID,
		Name:      config.Name,
		Transport: config.Transport,
		State:     "disabled",
		Message:   "服务器已停用",
	}
	server := &managedServer{config: config, key: key, tools: map[string]*sdkmcp.Tool{}, status: status}
	if !config.Enabled {
		return server
	}
	if config.Transport == TransportBuiltin || strings.HasPrefix(config.Command, "builtin:") {
		server.status.State = "ready"
		server.status.Message = "内置工具由 EchoCode 运行时提供"
		server.status.CheckedAt = time.Now().Format(time.RFC3339)
		return server
	}

	transport, stderr, err := transportForConfig(config)
	server.stderr = stderr
	if err != nil {
		server.status.State = "error"
		server.status.Message = err.Error()
		server.status.CheckedAt = time.Now().Format(time.RFC3339)
		return server
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "echocode", Title: "EchoCode", Version: "0.3.21"}, &sdkmcp.ClientOptions{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		KeepAlive: 30 * time.Second,
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			go func() {
				refreshCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				m.refreshTools(refreshCtx, config.ID)
			}()
		},
	})
	if rootURI := workspaceRootURI(config.WorkspaceRoot); rootURI != "" {
		client.AddRoots(&sdkmcp.Root{URI: rootURI, Name: filepath.Base(config.WorkspaceRoot)})
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		server.status.State = "error"
		server.status.Message = connectionErrorMessage(err, stderr)
		server.status.CheckedAt = time.Now().Format(time.RFC3339)
		return server
	}
	server.client = client
	server.session = session
	loaded, snapshot, err := fetchServerTools(ctx, session, config)
	if err != nil {
		_ = session.Close()
		server.session = nil
		server.status.State = "error"
		server.status.Message = connectionErrorMessage(err, stderr)
		server.status.CheckedAt = time.Now().Format(time.RFC3339)
		return server
	}
	server.tools = loaded
	server.snapshot = snapshot
	server.status.ToolCount = len(loaded)
	server.status.State = "ready"
	server.status.Message = fmt.Sprintf("连接正常，发现 %d 个工具", len(server.tools))
	server.status.CheckedAt = time.Now().Format(time.RFC3339)
	if initialized := session.InitializeResult(); initialized != nil {
		server.status.ProtocolVersion = initialized.ProtocolVersion
		if initialized.ServerInfo != nil {
			server.status.ServerVersion = strings.TrimSpace(initialized.ServerInfo.Version)
		}
	}
	return server
}

func (m *Manager) watchServer(server *managedServer) {
	if server == nil || server.session == nil {
		return
	}
	session := server.session
	err := session.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.servers[server.config.ID] != server {
		return
	}
	server.session = nil
	server.status.State = "error"
	server.status.Message = connectionErrorMessage(err, server.stderr)
	server.status.CheckedAt = time.Now().Format(time.RFC3339)
}

func (m *Manager) refreshTools(ctx context.Context, serverID string) {
	m.mu.RLock()
	server := m.servers[serverID]
	var session *sdkmcp.ClientSession
	var config ServerConfig
	if server != nil {
		session = server.session
		config = server.config
	}
	m.mu.RUnlock()
	if server == nil || session == nil {
		return
	}
	loaded, snapshot, err := fetchServerTools(ctx, session, config)
	if err != nil {
		m.mu.Lock()
		if m.servers[serverID] == server {
			server.status.Message = err.Error()
			server.status.CheckedAt = time.Now().Format(time.RFC3339)
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	if m.servers[serverID] == server {
		server.tools = loaded
		server.snapshot = snapshot
		server.status.ToolCount = len(loaded)
		server.status.State = "ready"
		server.status.Message = fmt.Sprintf("工具列表已更新，共 %d 个工具", len(server.tools))
		server.status.CheckedAt = time.Now().Format(time.RFC3339)
	}
	m.mu.Unlock()
}

func fetchServerTools(ctx context.Context, session *sdkmcp.ClientSession, config ServerConfig) (map[string]*sdkmcp.Tool, ServerSnapshot, error) {
	if session == nil {
		return nil, ServerSnapshot{}, errors.New("MCP 会话未连接")
	}
	loaded := map[string]*sdkmcp.Tool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, ServerSnapshot{}, fmt.Errorf("获取 MCP 工具失败: %w", err)
		}
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		loaded[tool.Name] = tool
	}
	descriptors := make([]ToolDescriptor, 0, len(loaded))
	for _, tool := range loaded {
		schema, _ := json.Marshal(tool.InputSchema)
		descriptors = append(descriptors, ToolDescriptor{
			Name:            namespacedToolName(config.ID, tool.Name),
			InputSchemaHash: HashSchema(string(schema)),
			OutputPolicy:    config.ToolResultPolicy,
		})
	}
	return loaded, NewServerSnapshot(config.ID, descriptors), nil
}

func (m *Manager) Tools() []tools.Tool {
	return m.ToolsForWorkspace("")
}

// ToolsForWorkspace binds per-session workspace context to remote tool
// wrappers without reconnecting the shared MCP server. This is especially
// important for background sessions: each runtime can query its own project
// while other sessions continue using the same server connection.
func (m *Manager) ToolsForWorkspace(workspaceRoot string) []tools.Tool {
	workspaceRoot = canonicalWorkspaceRoot(workspaceRoot)
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]tools.Tool, 0)
	for _, id := range m.order {
		server := m.servers[id]
		if server == nil || server.status.State != "ready" || server.session == nil {
			continue
		}
		names := make([]string, 0, len(server.tools))
		for name := range server.tools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			remote := server.tools[name]
			schema := schemaObject(remote.InputSchema)
			result = append(result, &RemoteTool{
				manager:           m,
				serverID:          id,
				remoteName:        name,
				name:              namespacedToolName(id, name),
				description:       strings.TrimSpace(remote.Description),
				schema:            schema,
				readOnly:          remote.Annotations != nil && remote.Annotations.ReadOnlyHint,
				workspaceRoot:     workspaceRoot,
				injectProjectPath: isLocalCodeGraphServer(server.config) && schemaHasProperty(schema, "projectPath"),
			})
		}
	}
	return result
}

func (m *Manager) Snapshots() []ServerSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ServerSnapshot, 0, len(m.order))
	for _, id := range m.order {
		server := m.servers[id]
		if server != nil && server.status.State == "ready" && len(server.snapshot.Tools) > 0 {
			result = append(result, server.snapshot)
		}
	}
	return result
}

func (m *Manager) Statuses(configs []ServerConfig) []ServerStatus {
	configs = normalizeServerConfigs(configs)
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ServerStatus, 0, len(configs))
	for _, config := range configs {
		if server := m.servers[config.ID]; server != nil {
			result = append(result, server.status)
			continue
		}
		status := ServerStatus{ID: config.ID, Name: config.Name, Transport: config.Transport, State: "idle", Message: "等待连接"}
		if !config.Enabled {
			status.State = "disabled"
			status.Message = "服务器已停用"
		} else if config.Transport == TransportBuiltin || strings.HasPrefix(config.Command, "builtin:") {
			status.State = "ready"
			status.Message = "内置工具由 EchoCode 运行时提供"
		}
		result = append(result, status)
	}
	return result
}

func (m *Manager) Close() {
	m.mu.Lock()
	servers := m.servers
	m.servers = map[string]*managedServer{}
	m.order = nil
	m.mu.Unlock()
	for _, server := range servers {
		closeManagedServer(server)
	}
}

func closeManagedServer(server *managedServer) {
	if server != nil && server.session != nil {
		_ = server.session.Close()
	}
}

func normalizeServerConfigs(configs []ServerConfig) []ServerConfig {
	result := make([]ServerConfig, 0, len(configs))
	seen := map[string]bool{}
	for _, config := range configs {
		config.ID = strings.TrimSpace(config.ID)
		if config.ID == "" || seen[config.ID] {
			continue
		}
		seen[config.ID] = true
		config.Name = strings.TrimSpace(config.Name)
		if config.Name == "" {
			config.Name = config.ID
		}
		config.Command = strings.TrimSpace(config.Command)
		config.URL = strings.TrimSpace(config.URL)
		config.WorkingDirectory = strings.TrimSpace(config.WorkingDirectory)
		config.WorkspaceRoot = strings.TrimSpace(config.WorkspaceRoot)
		if strings.HasPrefix(config.Command, "builtin:") {
			config.Transport = TransportBuiltin
		}
		switch config.Transport {
		case TransportBuiltin, TransportStdio, TransportStreamableHTTP, TransportSSE:
		default:
			config.Transport = TransportStdio
		}
		if config.ToolResultPolicy == "" {
			config.ToolResultPolicy = "summary-first"
		}
		result = append(result, config)
	}
	return result
}

func serverConfigKey(config ServerConfig) string {
	encoded, _ := json.Marshal(config)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func workspaceRootURI(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	path := filepath.ToSlash(absolute)
	if len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func canonicalWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return filepath.Clean(absolute)
}

func isLocalCodeGraphServer(config ServerConfig) bool {
	if config.Transport != TransportStdio {
		return false
	}
	command := strings.Trim(strings.TrimSpace(config.Command), "\"'")
	base := strings.ToLower(filepath.Base(command))
	switch filepath.Ext(base) {
	case ".exe", ".cmd", ".bat":
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if base != "codegraph" {
		return false
	}
	hasServe := false
	hasMCP := false
	for _, arg := range config.Args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "serve":
			hasServe = true
		case "--mcp":
			hasMCP = true
		}
	}
	return hasServe && hasMCP
}

func schemaHasProperty(schema map[string]any, name string) bool {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = properties[name]
	return ok
}

func namespacedToolName(serverID, toolName string) string {
	return "mcp__" + safeIdentifier(serverID) + "__" + safeIdentifier(toolName)
}

func safeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unnamed"
	}
	return builder.String()
}

func schemaObject(value any) map[string]any {
	if schema, ok := value.(map[string]any); ok {
		return schema
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		var schema map[string]any
		if json.Unmarshal(encoded, &schema) == nil && schema != nil {
			return schema
		}
	}
	return map[string]any{"type": "object", "additionalProperties": true}
}

func connectionErrorMessage(err error, stderr *tailBuffer) string {
	message := "MCP 连接已关闭"
	if err != nil {
		message = err.Error()
	}
	if stderr != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			message += ": " + detail
		}
	}
	return truncateText(message, 1200)
}
