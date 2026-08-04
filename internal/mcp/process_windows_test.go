//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPCommandSupportsCommandFilesWithoutConsoleWindow(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test mcp.cmd")
	content := "@echo off\r\necho %~1\r\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	command := mcpCommand(path, "ready")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run command MCP: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "ready" {
		t.Fatalf("output = %q", output)
	}
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("MCP command must hide its console window")
	}
}
