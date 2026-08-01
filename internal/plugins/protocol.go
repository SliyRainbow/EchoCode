package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type runnerLimits struct {
	maxExecutionSeconds int
	maxOutputBytes      int
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type externalCallResult struct {
	Summary           string             `json:"summary"`
	Content           any                `json:"content,omitempty"`
	StructuredContent any                `json:"structuredContent,omitempty"`
	IsError           bool               `json:"isError,omitempty"`
	Attachments       []tools.Attachment `json:"attachments,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Name            string `json:"name,omitempty"`
	Version         string `json:"version,omitempty"`
}

func runExternal(
	ctx context.Context,
	appVersion string,
	record record,
	descriptor ToolManifest,
	arguments map[string]any,
	policy tools.SandboxPolicy,
	grant PermissionGrant,
	limits runnerLimits,
) (tools.Result, error) {
	command, err := resolveRuntimeCommand(record)
	if err != nil {
		return tools.Result{}, err
	}
	cmd := exec.Command(command, record.manifest.Runtime.Args...)
	cmd.Dir = record.dir
	cmd.Env = pluginEnvironment()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return tools.Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tools.Result{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tools.Result{}, err
	}
	process, err := sandboxexec.Start(cmd, policy.ProcessLimits())
	if err != nil {
		return tools.Result{}, fmt.Errorf("启动插件进程失败: %w", err)
	}

	var (
		stderrBuffer boundedBuffer
		waitDone     = make(chan error, 1)
		terminate    sync.Once
	)
	stderrBuffer.limit = maxInt(64*1024, limits.maxOutputBytes/4)
	go func() {
		_, _ = io.Copy(&stderrBuffer, stderr)
	}()
	go func() { waitDone <- process.Wait() }()
	stop := func() {
		terminate.Do(func() { _ = process.Terminate() })
	}
	defer func() {
		_ = stdin.Close()
		stop()
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	}()

	encoder := json.NewEncoder(stdin)
	initializeID := "initialize-1"
	if err := encoder.Encode(rpcRequest{
		JSONRPC: "2.0",
		ID:      initializeID,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": ProtocolVersion,
			"host":            map[string]any{"name": "EchoCode", "version": strings.TrimSpace(appVersion)},
			"plugin":          map[string]any{"id": record.manifest.ID, "version": record.manifest.Version},
		},
	}); err != nil {
		return tools.Result{}, fmt.Errorf("发送插件初始化请求失败: %w", err)
	}
	reader := bufio.NewReaderSize(stdout, 64*1024)
	response, err := readRPCResponse(ctx, reader, initializeID, limits.maxOutputBytes)
	if err != nil {
		return tools.Result{}, enrichPluginProcessError(err, &stderrBuffer)
	}
	if response.Error != nil {
		return tools.Result{}, fmt.Errorf("插件初始化失败 (%d): %s", response.Error.Code, response.Error.Message)
	}
	var initialized initializeResult
	if err := json.Unmarshal(response.Result, &initialized); err != nil {
		return tools.Result{}, fmt.Errorf("插件初始化结果无效: %w", err)
	}
	if initialized.ProtocolVersion != ProtocolVersion {
		return tools.Result{}, fmt.Errorf("插件协议版本不兼容: plugin=%q, host=%q", initialized.ProtocolVersion, ProtocolVersion)
	}

	callID := "call-1"
	if err := encoder.Encode(rpcRequest{
		JSONRPC: "2.0",
		ID:      callID,
		Method:  "tools.call",
		Params: map[string]any{
			"name":      descriptor.Name,
			"arguments": arguments,
			"context": map[string]any{
				"workspaceRoot": policy.WorkspaceRoot,
				"permissions":   grant,
			},
		},
	}); err != nil {
		return tools.Result{}, fmt.Errorf("发送插件工具请求失败: %w", err)
	}
	_ = stdin.Close()
	response, err = readRPCResponse(ctx, reader, callID, limits.maxOutputBytes)
	if err != nil {
		return tools.Result{}, enrichPluginProcessError(err, &stderrBuffer)
	}
	if response.Error != nil {
		return tools.Result{}, fmt.Errorf("插件工具失败 (%d): %s", response.Error.Code, response.Error.Message)
	}
	var callResult externalCallResult
	if len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, &callResult); err != nil {
			return tools.Result{}, fmt.Errorf("插件返回了无效 tools.call 结果: %w", err)
		}
	}
	if callResult.Summary == "" {
		callResult.Summary = summarizeExternalContent(callResult.Content, callResult.StructuredContent)
	}
	if callResult.Summary == "" {
		callResult.Summary = "插件工具执行完成"
	}
	for _, attachment := range callResult.Attachments {
		if strings.TrimSpace(attachment.Name) == "" || strings.TrimSpace(attachment.MIMEType) == "" {
			return tools.Result{}, errors.New("插件返回了无效附件")
		}
		if _, err := base64.StdEncoding.DecodeString(attachment.Data); err != nil {
			return tools.Result{}, fmt.Errorf("插件附件 %q 不是有效 base64: %w", attachment.Name, err)
		}
	}
	return tools.Result{
		Summary:     boundedText(callResult.Summary, limits.maxOutputBytes),
		IsError:     callResult.IsError,
		Attachments: callResult.Attachments,
	}, nil
}

func readRPCResponse(ctx context.Context, reader *bufio.Reader, expectedID string, limit int) (rpcResponse, error) {
	type outcome struct {
		response rpcResponse
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		total := 0
		for {
			line, err := readBoundedLine(reader, limit)
			if err != nil {
				result <- outcome{err: err}
				return
			}
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			total += len(line)
			if total > limit {
				result <- outcome{err: fmt.Errorf("插件 stdout 超过 %d 字节上限", limit)}
				return
			}
			var response rpcResponse
			if err := json.Unmarshal(line, &response); err != nil {
				result <- outcome{err: fmt.Errorf("插件 stdout 不是有效 JSONL: %w", err)}
				return
			}
			if response.JSONRPC != "2.0" {
				result <- outcome{err: errors.New("插件响应缺少 jsonrpc=2.0")}
				return
			}
			if response.ID != expectedID {
				continue
			}
			result <- outcome{response: response}
			return
		}
	}()
	select {
	case <-ctx.Done():
		return rpcResponse{}, ctx.Err()
	case value := <-result:
		return value.response, value.err
	}
}

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	if limit < 64*1024 {
		limit = 64 * 1024
	}
	var result []byte
	for {
		fragment, prefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(result)+len(fragment) > limit {
			return nil, fmt.Errorf("插件 stdout 单条消息超过 %d 字节上限", limit)
		}
		result = append(result, fragment...)
		if !prefix {
			return result, nil
		}
	}
}

func resolveRuntimeCommand(record record) (string, error) {
	command := strings.TrimSpace(record.manifest.Runtime.Command)
	if command == "" {
		return "", errors.New("plugin runtime command is empty")
	}
	if filepath.IsAbs(command) {
		if info, err := os.Stat(command); err != nil || info.IsDir() {
			return "", fmt.Errorf("plugin runtime command is unavailable: %s", command)
		}
		return command, nil
	}
	if strings.ContainsAny(command, `/\`) {
		absolute := filepath.Clean(filepath.Join(record.dir, command))
		if within, err := pathWithin(record.dir, absolute); err != nil || !within {
			return "", errors.New("plugin runtime command escapes the plugin directory")
		}
		if info, err := os.Stat(absolute); err != nil || info.IsDir() {
			return "", fmt.Errorf("plugin runtime command is unavailable: %s", command)
		}
		return absolute, nil
	}
	local := filepath.Join(record.dir, command)
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return local, nil
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("plugin runtime command %q was not found in PATH", command)
	}
	return resolved, nil
}

func runBuiltinWorker(
	ctx context.Context,
	appVersion string,
	record record,
	descriptor ToolManifest,
	arguments map[string]any,
	policy tools.SandboxPolicy,
	grant PermissionGrant,
	limits runnerLimits,
) (tools.Result, error) {
	executable, err := os.Executable()
	if err != nil {
		return tools.Result{}, fmt.Errorf("定位 EchoCode 插件工作进程失败: %w", err)
	}
	worker := record
	worker.dir = filepath.Dir(executable)
	worker.manifest.Runtime = Runtime{
		Transport: "stdio",
		Command:   executable,
		Args:      []string{"--echocode-plugin-worker", record.manifest.ID},
	}
	return runExternal(ctx, appVersion, worker, descriptor, arguments, policy, grant, limits)
}

func pathWithin(root, target string) (bool, error) {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false, err
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func pluginEnvironment() []string {
	result := make([]string, 0, 8)
	for _, key := range []string{"PATH", "Path", "PATHEXT", "SYSTEMROOT", "SystemRoot", "TEMP", "TMP", "HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func summarizeExternalContent(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if encoded, err := json.Marshal(value); err == nil && string(encoded) != "null" {
			return string(encoded)
		}
	}
	return ""
}

func enrichPluginProcessError(err error, stderr *boundedBuffer) error {
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		return err
	}
	return fmt.Errorf("%w; plugin stderr: %s", err, boundedText(message, 4000))
}

type boundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
			b.truncated = true
		}
		_, _ = b.buffer.Write(data)
	} else {
		b.truncated = true
	}
	return original, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	value := b.buffer.String()
	if b.truncated {
		value += "\n... [truncated]"
	}
	return value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
