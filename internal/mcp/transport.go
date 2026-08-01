package mcp

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func transportForConfig(config ServerConfig) (sdkmcp.Transport, *tailBuffer, error) {
	switch config.Transport {
	case TransportStdio:
		if config.Command == "" {
			return nil, nil, fmt.Errorf("MCP %s 缺少启动命令", config.Name)
		}
		command := exec.Command(config.Command, config.Args...)
		command.Dir = config.WorkingDirectory
		if command.Dir == "" {
			command.Dir = config.WorkspaceRoot
		}
		command.Env = childEnvironment(config)
		stderr := &tailBuffer{limit: 16 * 1024}
		command.Stderr = stderr
		return &sdkmcp.CommandTransport{Command: command, TerminateDuration: 3 * time.Second}, stderr, nil
	case TransportStreamableHTTP, TransportSSE:
		if !config.AllowNetwork {
			return nil, nil, fmt.Errorf("网络访问已关闭，不能连接远程 MCP 服务器")
		}
		if config.URL == "" {
			return nil, nil, fmt.Errorf("MCP %s 缺少服务器 URL", config.Name)
		}
		client := mcpHTTPClient(config.Headers)
		if config.Transport == TransportSSE {
			return &sdkmcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}, nil, nil
		}
		return &sdkmcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: 5}, nil, nil
	default:
		return nil, nil, fmt.Errorf("不支持的 MCP transport: %s", config.Transport)
	}
}

func childEnvironment(config ServerConfig) []string {
	allowed := []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "COMSPEC"}
	allowed = append(allowed, config.PassEnvironment...)
	values := map[string]string{}
	originalKeys := map[string]string{}
	for _, key := range allowed {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			normalized := strings.ToUpper(key)
			values[normalized] = value
			originalKeys[normalized] = key
		}
	}
	for _, item := range config.Env {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		normalized := strings.ToUpper(key)
		values[normalized] = item.Value
		originalKeys[normalized] = key
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, originalKeys[key]+"="+values[key])
	}
	return result
}

func mcpHTTPClient(headers []KeyValue) *http.Client {
	base := http.DefaultTransport
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		clone := transport.Clone()
		clone.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		clone.TLSHandshakeTimeout = 15 * time.Second
		clone.ResponseHeaderTimeout = 30 * time.Second
		base = clone
	}
	return &http.Client{Transport: &headerRoundTripper{base: base, headers: headers}}
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers []KeyValue
}

func (t *headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for _, item := range t.headers {
		if key := strings.TrimSpace(item.Key); key != "" {
			clone.Header.Set(key, item.Value)
		}
	}
	return t.base.RoundTrip(clone)
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if b.limit > 0 && len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}
