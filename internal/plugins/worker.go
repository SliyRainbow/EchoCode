package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const workerArgument = "--echocode-plugin-worker"

// HandleCommandLine runs a first-party plugin worker before the desktop UI is
// initialized. The worker uses stdin/stdout JSON-RPC and is always owned by a
// host process that can terminate it on cancellation or timeout.
func HandleCommandLine(args []string) (bool, int) {
	if len(args) < 2 || args[0] != workerArgument {
		return false, 0
	}
	if err := serveBuiltinWorker(context.Background(), strings.TrimSpace(args[1]), os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		return true, 1
	}
	return true, 0
}

func serveBuiltinWorker(ctx context.Context, pluginID string, input io.Reader, output io.Writer) error {
	manifest, ok := builtinManifestByID(pluginID)
	if !ok {
		return fmt.Errorf("unknown built-in plugin %q", pluginID)
	}
	reader := bufio.NewReaderSize(input, 64*1024)
	encoder := json.NewEncoder(output)
	initialized := false
	for {
		line, err := readBoundedLine(reader, 16*1024*1024)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal(line, &request); err != nil {
			return fmt.Errorf("decode worker request: %w", err)
		}
		response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			encoded, _ := json.Marshal(request.Params)
			_ = json.Unmarshal(encoded, &params)
			if params.ProtocolVersion != ProtocolVersion {
				response.Error = &rpcError{Code: -32001, Message: "unsupported protocol version"}
			} else {
				initialized = true
				response.Result, _ = json.Marshal(initializeResult{ProtocolVersion: ProtocolVersion, Name: manifest.Name, Version: manifest.Version})
			}
		case "tools.call":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "worker is not initialized"}
				break
			}
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			encoded, _ := json.Marshal(request.Params)
			if err := json.Unmarshal(encoded, &params); err != nil {
				response.Error = &rpcError{Code: -32602, Message: err.Error()}
				break
			}
			if !manifestHasTool(manifest, params.Name) {
				response.Error = &rpcError{Code: -32601, Message: "tool is not declared by the built-in plugin"}
				break
			}
			rawArgs, _ := json.Marshal(params.Arguments)
			result, callErr := executeBuiltin(ctx, manifest.ID, params.Name, rawArgs)
			if callErr != nil {
				response.Error = &rpcError{Code: -32000, Message: callErr.Error()}
				break
			}
			response.Result, _ = json.Marshal(externalCallResult{
				Summary:     result.Summary,
				IsError:     result.IsError,
				Attachments: result.Attachments,
			})
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not found"}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
}

func builtinManifestByID(id string) (Manifest, bool) {
	for _, manifest := range builtinManifests() {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return Manifest{}, false
}

func manifestHasTool(manifest Manifest, name string) bool {
	for _, descriptor := range manifest.Tools {
		if descriptor.Name == name {
			return true
		}
	}
	return false
}

var _ = tools.Result{}
