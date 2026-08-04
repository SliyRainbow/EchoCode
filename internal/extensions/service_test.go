package extensions

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogRefreshFallsBackToCache(t *testing.T) {
	t.Parallel()
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if failing.Load() {
			http.Error(writer, "offline", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/registry.json":
			_, _ = writer.Write([]byte(`{"schemaVersion":1,"name":"test","description":"test","packages":[{"id":"mcp.test","type":"mcp","name":"Test","summary":"Test package","publisher":"Test","manifest":"manifest.json","featured":true,"sourceVerified":true}]}`))
		case "/manifest.json":
			_, _ = writer.Write([]byte(testManifestJSON("https://example.invalid/test.zip", strings.Repeat("a", 64))))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "catalog.json")
	service := New(Options{
		RegistryURL: server.URL + "/registry.json",
		CachePath:   cachePath,
		InstallRoot: t.TempDir(),
		HTTPClient:  server.Client(),
	})
	state, err := service.Catalog(context.Background(), true)
	if err != nil {
		t.Fatalf("Catalog(network): %v", err)
	}
	if state.Source != "network" || len(state.Packages) != 1 {
		t.Fatalf("network catalog = %#v", state)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("catalog cache missing: %v", err)
	}

	failing.Store(true)
	cached, err := service.Catalog(context.Background(), true)
	if err != nil {
		t.Fatalf("Catalog(cache fallback): %v", err)
	}
	if cached.Source != "cache" || !strings.Contains(cached.Warning, "正在使用缓存") {
		t.Fatalf("cache fallback = %#v", cached)
	}
}

func TestFetchJSONRejectsOversizedCatalog(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schemaVersion":1}` + strings.Repeat(" ", maxCatalogBytes)))
	}))
	defer server.Close()

	service := New(Options{HTTPClient: server.Client()})
	var registry Registry
	err := service.fetchJSON(context.Background(), server.URL, &registry)
	if err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("fetchJSON error = %v, want size limit", err)
	}
}

func TestWriteJSONAtomicReplacesExistingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	service := New(Options{InstallRoot: t.TempDir()})
	if err := service.writeJSONAtomic(path, map[string]int{"value": 1}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := service.writeJSONAtomic(path, map[string]int{"value": 2}); err != nil {
		t.Fatalf("replacement write: %v", err)
	}
	var value map[string]int
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["value"] != 2 {
		t.Fatalf("state = %#v, want replacement", value)
	}
}

func TestNewPreservesEmptyInstallRoot(t *testing.T) {
	t.Parallel()
	service := New(Options{})
	if service.installRoot != "" {
		t.Fatalf("install root = %q, want empty", service.installRoot)
	}
}

func TestExtractZIPRejectsTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	archivePath := filepath.Join(base, "bad.zip")
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	entry, err := writer.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("escaped"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, payload.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "unpacked")
	if err := extractZIP(archivePath, destination); err == nil {
		t.Fatal("expected ZIP traversal to be rejected")
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal file was created: %v", err)
	}
}

func TestExtractTarGZRejectsTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	archivePath := filepath.Join(base, "bad.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("escaped")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../escaped.txt", Mode: 0o600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "unpacked")
	if err := extractTarGZ(archivePath, destination); err == nil {
		t.Fatal("expected tar traversal to be rejected")
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal file was created: %v", err)
	}
}

func TestInstallRejectsChecksumMismatchWithoutState(t *testing.T) {
	t.Parallel()
	archive := testZIP(t, map[string]string{"package/bin/tool": "tool"})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	service, cachePath, installRoot := testInstallService(t, server.Client())
	writeTestCatalog(t, service, cachePath, server.URL+"/tool.zip", strings.Repeat("0", 64))
	if _, err := service.Install(context.Background(), "mcp.test"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Install error = %v, want checksum failure", err)
	}
	if _, err := os.Stat(filepath.Join(installRoot, "installed.json")); !os.IsNotExist(err) {
		t.Fatalf("installed state must not be written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installRoot, "mcp.test")); !os.IsNotExist(err) {
		t.Fatalf("package directory must not be committed: %v", err)
	}
}

func TestInstallAndUninstallPreserveProjectData(t *testing.T) {
	t.Parallel()
	executableRelative := "bin/tool"
	if runtime.GOOS == "windows" {
		executableRelative += ".exe"
	}
	archive := testZIP(t, map[string]string{"package/" + executableRelative: "tool"})
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	service, cachePath, _ := testInstallService(t, server.Client())
	writeTestCatalogWithExecutable(t, service, cachePath, server.URL+"/tool.zip", hex.EncodeToString(digest[:]), executableRelative)
	result, err := service.Install(context.Background(), "mcp.test")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(result.Package.Executable); err != nil {
		t.Fatalf("installed executable missing: %v", err)
	}

	workspace := t.TempDir()
	indexPath := filepath.Join(workspace, ".codegraph", "index.db")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.Uninstall("mcp.test"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(result.Package.InstallDir); !os.IsNotExist(err) {
		t.Fatalf("install directory still exists: %v", err)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("project index was removed: %v", err)
	}
}

func testInstallService(t *testing.T, client *http.Client) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	cachePath := filepath.Join(root, "catalog.json")
	installRoot := filepath.Join(root, "extensions")
	return New(Options{CachePath: cachePath, InstallRoot: installRoot, HTTPClient: client}), cachePath, installRoot
}

func writeTestCatalog(t *testing.T, service *Service, cachePath, artifactURL, checksum string) {
	t.Helper()
	executable := "bin/tool"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	writeTestCatalogWithExecutable(t, service, cachePath, artifactURL, checksum, executable)
}

func writeTestCatalogWithExecutable(t *testing.T, service *Service, cachePath, artifactURL, checksum, executable string) {
	t.Helper()
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "mcp.test",
		Type:          "mcp",
		Name:          "Test MCP",
		Version:       "1.0.0",
		Artifacts: []Artifact{{
			Platform:    runtime.GOOS,
			Arch:        arch,
			Archive:     "zip",
			URL:         artifactURL,
			SHA256:      checksum,
			ArchiveRoot: "package",
			Executable:  executable,
		}},
		Install: InstallSpec{HealthCheck: HealthCheck{}},
		MCP:     &MCPConfig{Transport: "stdio", Command: "${artifactExecutable}"},
	}
	state := CatalogState{RegistryURL: "test", Source: "cache", Packages: []CatalogPackage{{
		ID:                manifest.ID,
		Type:              manifest.Type,
		Name:              manifest.Name,
		Manifest:          manifest,
		PlatformAvailable: true,
	}}}
	if err := service.writeJSONAtomic(cachePath, state); err != nil {
		t.Fatal(err)
	}
}

func testZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	for name, content := range files {
		header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
		header.SetMode(0o755)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func testManifestJSON(artifactURL, checksum string) string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return fmt.Sprintf(`{"schemaVersion":1,"id":"mcp.test","type":"mcp","name":"Test","version":"1.0.0","channel":"stable","summary":"Test","description":"Test","publisher":{"name":"Test","url":"https://example.invalid"},"source":{"repository":"https://example.invalid","release":"https://example.invalid/v1","thirdParty":true},"license":{"spdx":"MIT","file":"LICENSE"},"categories":[],"capabilities":[],"permissions":[],"artifacts":[{"platform":%q,"arch":%q,"archive":"zip","url":%q,"sha256":%q,"archiveRoot":"package","executable":"bin/tool"}],"verification":{"checksums":"https://example.invalid/sums","attestationRepository":"test/test"},"install":{"directory":"mcp/test","healthCheck":{"args":[],"timeoutSeconds":0}},"mcp":{"transport":"stdio","command":"${artifactExecutable}","args":[],"env":[],"workingDirectory":"","toolResultPolicy":"summary-first"},"uninstall":{"removeInstallDirectory":true,"preserveProjectPaths":[]}}`, runtime.GOOS, arch, artifactURL, checksum)
}

func TestRunHealthCheckHonorsTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	cancel()
	if err := runHealthCheck(ctx, "missing-extension", HealthCheck{Args: []string{"version"}, TimeoutSeconds: 1}); err == nil {
		t.Fatal("expected cancelled health check to fail")
	}
}
