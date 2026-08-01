package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestExternalPluginProtocolRoundTrip(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "success")
	workspace := t.TempDir()
	result, err := runExternal(
		context.Background(),
		"9.8.7",
		record,
		descriptor,
		map[string]any{"value": "hello"},
		testPluginPolicy(workspace),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("echo:hello host=9.8.7 workspace=%s", workspace)
	if result.Summary != want || result.IsError {
		t.Fatalf("result = %#v, want summary %q", result, want)
	}
}

func TestExternalPluginProtocolHonorsDeadline(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runExternal(
		ctx,
		"test",
		record,
		descriptor,
		map[string]any{},
		testPluginPolicy(t.TempDir()),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("deadline cancellation took %s", elapsed)
	}
}

func TestExternalPluginProtocolHonorsCancellation(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	_, err := runExternal(
		ctx,
		"test",
		record,
		descriptor,
		map[string]any{},
		testPluginPolicy(t.TempDir()),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestExternalPluginProtocolIncludesStderr(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "stderr")
	_, err := runExternal(
		context.Background(),
		"test",
		record,
		descriptor,
		map[string]any{},
		testPluginPolicy(t.TempDir()),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if err == nil || !strings.Contains(err.Error(), "plugin stderr: deliberate helper failure") {
		t.Fatalf("error = %v, want bounded stderr detail", err)
	}
}

func TestExternalPluginProtocolRejectsOversizedOutput(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "oversized")
	_, err := runExternal(
		context.Background(),
		"test",
		record,
		descriptor,
		map[string]any{},
		testPluginPolicy(t.TempDir()),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if err == nil || !strings.Contains(err.Error(), "超过 65536 字节上限") {
		t.Fatalf("error = %v, want output limit failure", err)
	}
}

func TestExternalPluginProtocolRejectsInvalidAttachment(t *testing.T) {
	record, descriptor := externalTestPlugin(t, "invalid-attachment")
	_, err := runExternal(
		context.Background(),
		"test",
		record,
		descriptor,
		map[string]any{},
		testPluginPolicy(t.TempDir()),
		PermissionGrant{FileRead: true},
		runnerLimits{maxExecutionSeconds: 5, maxOutputBytes: 64 * 1024},
	)
	if err == nil || !strings.Contains(err.Error(), "不是有效 base64") {
		t.Fatalf("error = %v, want invalid attachment failure", err)
	}
}

func externalTestPlugin(t *testing.T, mode string) (record, ToolManifest) {
	t.Helper()
	descriptor := ToolManifest{
		Name:        "echo",
		Description: "Echo a value",
		InputSchema: map[string]any{"type": "object"},
		ReadOnly:    true,
		Permissions: PermissionSpec{FileRead: true},
	}
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "protocol-test",
		Name:          "Protocol Test",
		Version:       "1.0.0",
		Runtime: Runtime{
			Transport: "stdio",
			Command:   os.Args[0],
			Args:      []string{"-test.run=^TestPluginProtocolHelperProcess$", "--", mode},
		},
		Permissions: PermissionSpec{FileRead: true},
		Tools:       []ToolManifest{descriptor},
	}
	return record{manifest: manifest, dir: t.TempDir(), source: "installed"}, descriptor
}

func testPluginPolicy(workspace string) tools.SandboxPolicy {
	return tools.SandboxPolicy{
		SandboxMode:          "danger-full-access",
		WorkspaceRoot:        workspace,
		FilesystemAccess:     "workspace-write",
		MaxCommandMemoryMB:   512,
		MaxCommandCPUPercent: 100,
		MaxCommandProcesses:  8,
	}
}

// TestPluginProtocolHelperProcess is re-executed as the external plugin. The
// argument after "--" selects deterministic protocol behavior for its parent.
func TestPluginProtocolHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	reader := bufio.NewReaderSize(os.Stdin, 64*1024)
	encoder := json.NewEncoder(os.Stdout)

	initialize := helperReadRequest(reader)
	var initializeParams struct {
		ProtocolVersion string `json:"protocolVersion"`
		Host            struct {
			Version string `json:"version"`
		} `json:"host"`
	}
	helperDecodeParams(initialize.Params, &initializeParams)
	if initialize.Method != "initialize" || initializeParams.ProtocolVersion != ProtocolVersion {
		os.Exit(21)
	}
	helperWriteResponse(encoder, initialize.ID, initializeResult{ProtocolVersion: ProtocolVersion})

	call := helperReadRequest(reader)
	if call.Method != "tools.call" {
		os.Exit(22)
	}
	switch mode {
	case "success":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Context   struct {
				WorkspaceRoot string `json:"workspaceRoot"`
			} `json:"context"`
		}
		helperDecodeParams(call.Params, &params)
		helperWriteResponse(encoder, call.ID, externalCallResult{
			Summary: fmt.Sprintf("%s:%v host=%s workspace=%s", params.Name, params.Arguments["value"], initializeParams.Host.Version, params.Context.WorkspaceRoot),
		})
	case "hang":
		time.Sleep(30 * time.Second)
	case "stderr":
		_, _ = fmt.Fprintln(os.Stderr, "deliberate helper failure")
		time.Sleep(50 * time.Millisecond)
		os.Exit(23)
	case "oversized":
		helperWriteResponse(encoder, call.ID, externalCallResult{Summary: strings.Repeat("x", 70*1024)})
	case "invalid-attachment":
		helperWriteResponse(encoder, call.ID, externalCallResult{
			Summary:     "invalid attachment",
			Attachments: []tools.Attachment{{Name: "bad.png", MIMEType: "image/png", Data: "not-base64"}},
		})
	default:
		os.Exit(24)
	}
	os.Exit(0)
}

func helperReadRequest(reader *bufio.Reader) rpcRequest {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		os.Exit(25)
	}
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		os.Exit(26)
	}
	return request
}

func helperDecodeParams(value any, target any) {
	encoded, err := json.Marshal(value)
	if err != nil || json.Unmarshal(encoded, target) != nil {
		os.Exit(27)
	}
}

func helperWriteResponse(encoder *json.Encoder, id string, result any) {
	encoded, err := json.Marshal(result)
	if err != nil || encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: encoded}) != nil {
		os.Exit(28)
	}
}
