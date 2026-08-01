package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestStructuredTransferToolsReachMainAndWorkerRegistries(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "用户指定目录")
	for _, directory := range []string{workspace, external} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.NetworkAccess = true
	service.runtimeSettings.ShellAccess = false
	service.runtimeSettings.ApprovalPolicy = "never"
	ctx := withTurnWritableRoots(context.Background(), []string{external})

	mainRegistry := service.buildToolRegistryForContext(ctx)
	workerRegistry := service.buildWorkerToolRegistryForContext(ctx)
	for _, registry := range []*tools.Registry{mainRegistry, workerRegistry} {
		for _, name := range []string{"download_file", "git_repository"} {
			if _, ok := registry.Get(name); !ok {
				t.Fatalf("%s missing from mutable registry", name)
			}
		}
	}
	readOnly := service.buildReadOnlyRegistryForContext(ctx)
	for _, name := range []string{"download_file", "git_repository"} {
		if _, ok := readOnly.Get(name); ok {
			t.Fatalf("%s must not be available during read-only planning", name)
		}
	}
}

func TestStructuredSearchToolsReachMainWorkerAndPlanRegistries(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	registries := map[string]*tools.Registry{
		"main":   service.buildToolRegistryForContext(context.Background()),
		"worker": service.buildWorkerToolRegistryForContext(context.Background()),
		"plan":   service.buildReadOnlyRegistryForContext(context.Background()),
	}
	for label, registry := range registries {
		for _, name := range []string{"grep", "glob"} {
			if _, ok := registry.Get(name); !ok {
				t.Fatalf("%s registry is missing %s", label, name)
			}
		}
	}
}

func TestStablePromptRequiresVerifiedStructuredTransfers(t *testing.T) {
	prompt := formatStablePrompt(RequestContext{})
	for _, expected := range []string{
		"use git_repository for clone, fetch, or pull",
		"use download_file instead of curl, wget, PowerShell, browser downloads, or shell redirection",
		"first inspect the vendor's official product or download page with read_webpage",
		"operating system, CPU architecture, release channel, version, and package format",
		"A product page, redirect landing page, search result, or HTML response is not an installer or archive",
		"Downloading an executable, installer, or script and running or installing it are separate actions",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("stable prompt is missing transfer rule %q", expected)
		}
	}
}

func TestTransferApprovalProgressUsesRedactedDisplayInput(t *testing.T) {
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{name: "download_file", args: map[string]any{
			"url": "https://user:password@example.com/app.exe?token=temporary-secret", "destination": "app.exe",
		}},
		{name: "git_repository", args: map[string]any{
			"action": "clone", "url": "https://user:password@example.com/demo.git?token=temporary-secret", "destination": "demo",
		}},
		{name: "browser", args: map[string]any{
			"action": "open", "url": "https://user:password@example.com/download?token=temporary-secret",
		}},
	} {
		raw, _ := json.Marshal(test.args)
		var progress tools.ResultPart
		ctx := tools.WithProgressSink(context.Background(), func(part tools.ResultPart) { progress = part })
		emitApprovalProgress(ctx, test.name, raw, "waiting", "waiting")
		for _, secret := range []string{"user", "password", "temporary-secret"} {
			if strings.Contains(progress.Input, secret) {
				t.Fatalf("%s approval progress leaked %q: %#v", test.name, secret, progress)
			}
		}
		if !strings.Contains(progress.Input, "https://example.com/") {
			t.Fatalf("%s approval progress lost safe source: %#v", test.name, progress)
		}
	}
}

func TestTurnAuthorizedExternalDownloadIsRegisteredAsArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("external artifact"))
	}))
	defer server.Close()

	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "D盘下载")
	for _, directory := range []string{workspace, external} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.NetworkAccess = true
	service.runtimeSettings.ApprovalPolicy = "never"
	service.recordUserEvent("download the file to the explicitly authorized external directory")

	destination := filepath.Join(external, "package.bin")
	arguments, _ := json.Marshal(map[string]any{"url": server.URL, "destination": destination})
	ctx := withTurnWritableRoots(context.Background(), []string{external})
	result, _ := service.executeToolCall(ctx, service.buildToolRegistryForContext(ctx), protocol.ToolCall{
		ID: "download-external", Type: "function",
		Function: protocol.ToolCallFunction{Name: "download_file", Arguments: arguments},
	})
	if result.IsError {
		t.Fatalf("external download failed: %#v", result)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "external artifact" {
		t.Fatalf("external artifact=%q err=%v", content, err)
	}
	records := service.ListSessionArtifacts()
	if len(records) != 1 || artifactPathKey(records[0].Path) != artifactPathKey(destination) || records[0].Tool != "download_file" || records[0].ToolCallID != "download-external" {
		t.Fatalf("external artifact records=%#v", records)
	}
}
