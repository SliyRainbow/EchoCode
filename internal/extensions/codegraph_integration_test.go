package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	internalmcp "github.com/SliyRainbow/EchoCode/internal/mcp"
)

func TestCodeGraphReleaseArchiveIntegration(t *testing.T) {
	archivePath := os.Getenv("ECHOCODE_CODEGRAPH_ARCHIVE")
	if archivePath == "" {
		t.Skip("set ECHOCODE_CODEGRAPH_ARCHIVE to run the CodeGraph release integration test")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("the supplied release fixture is the Windows x64 archive")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := file.WriteTo(hash); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	checksum := hex.EncodeToString(hash.Sum(nil))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.ServeFile(writer, request, archivePath)
	}))
	defer server.Close()

	root := t.TempDir()
	service := New(Options{
		CachePath:   filepath.Join(root, "catalog.json"),
		InstallRoot: filepath.Join(root, "extensions"),
		HTTPClient:  server.Client(),
	})
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "mcp.codegraph",
		Type:          "mcp",
		Name:          "CodeGraph",
		Version:       "1.5.0",
		Artifacts: []Artifact{{
			Platform:    "windows",
			Arch:        "x64",
			Archive:     "zip",
			URL:         server.URL + "/codegraph-win32-x64.zip",
			SHA256:      checksum,
			ArchiveRoot: "codegraph-win32-x64",
			Executable:  "bin/codegraph.cmd",
		}},
		Install: InstallSpec{HealthCheck: HealthCheck{Args: []string{"version"}, TimeoutSeconds: 15}},
		MCP: &MCPConfig{
			Transport:        "stdio",
			Command:          "${artifactExecutable}",
			Args:             []string{"serve", "--mcp"},
			Env:              []KeyValue{{Key: "CODEGRAPH_NO_DAEMON", Value: "1"}, {Key: "CODEGRAPH_TELEMETRY", Value: "0"}},
			ToolResultPolicy: "summary-first",
		},
	}
	catalog := CatalogState{RegistryURL: "integration", Source: "cache", Packages: []CatalogPackage{{
		ID:                manifest.ID,
		Type:              manifest.Type,
		Name:              manifest.Name,
		Manifest:          manifest,
		PlatformAvailable: true,
	}}}
	if err := service.writeJSONAtomic(service.cachePath, catalog); err != nil {
		t.Fatal(err)
	}

	installCtx, cancelInstall := context.WithTimeout(context.Background(), 90*time.Second)
	result, err := service.Install(installCtx, manifest.ID)
	cancelInstall()
	if err != nil {
		t.Fatalf("install CodeGraph release: %v", err)
	}
	if _, err := os.Stat(result.Package.Executable); err != nil {
		t.Fatalf("installed command missing: %v", err)
	}

	manager := internalmcp.NewManager()
	defer manager.Close()
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 45*time.Second)
	statuses := manager.Configure(connectCtx, []internalmcp.ServerConfig{{
		ID:               manifest.ID,
		Name:             manifest.Name,
		Transport:        internalmcp.TransportStdio,
		Command:          result.Package.Executable,
		Args:             []string{"serve", "--mcp"},
		Env:              []internalmcp.KeyValue{{Key: "CODEGRAPH_NO_DAEMON", Value: "1"}, {Key: "CODEGRAPH_TELEMETRY", Value: "0"}},
		Enabled:          true,
		WorkspaceRoot:    t.TempDir(),
		ToolResultPolicy: "summary-first",
	}})
	cancelConnect()
	if len(statuses) != 1 || statuses[0].State != "ready" || statuses[0].ToolCount == 0 {
		t.Fatalf("CodeGraph MCP status = %#v", statuses)
	}
}

func TestPublishedCodeGraphRegistryIntegration(t *testing.T) {
	if os.Getenv("ECHOCODE_EXTENSION_ONLINE_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_EXTENSION_ONLINE_INTEGRATION=1 to test the published registry")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("the published integration test currently verifies Windows x64")
	}

	root := t.TempDir()
	service := New(Options{
		CachePath:   filepath.Join(root, "catalog.json"),
		InstallRoot: filepath.Join(root, "extensions"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	catalog, err := service.Catalog(ctx, true)
	if err != nil {
		t.Fatalf("load published registry: %v", err)
	}
	item, ok := findCatalogPackage(catalog.Packages, "mcp.codegraph")
	if !ok || catalog.Source != "network" || item.Manifest.Version != "1.5.0" {
		t.Fatalf("published catalog = %#v", catalog)
	}
	result, err := service.Install(ctx, item.ID)
	if err != nil {
		t.Fatalf("install from published registry: %v", err)
	}

	manager := internalmcp.NewManager()
	defer manager.Close()
	statuses := manager.Configure(ctx, []internalmcp.ServerConfig{{
		ID:               item.ID,
		Name:             item.Name,
		Transport:        internalmcp.TransportStdio,
		Command:          result.Package.Executable,
		Args:             []string{"serve", "--mcp"},
		Env:              []internalmcp.KeyValue{{Key: "CODEGRAPH_NO_DAEMON", Value: "1"}, {Key: "CODEGRAPH_TELEMETRY", Value: "0"}},
		Enabled:          true,
		WorkspaceRoot:    t.TempDir(),
		ToolResultPolicy: "summary-first",
	}})
	if len(statuses) != 1 || statuses[0].State != "ready" || statuses[0].ToolCount == 0 {
		t.Fatalf("published CodeGraph MCP status = %#v", statuses)
	}
}
