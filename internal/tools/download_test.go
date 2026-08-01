package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadFileToolStreamsAndInstallsAtomically(t *testing.T) {
	payload := []byte("echocode structured download\n")
	digest := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = response.Write(payload)
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "downloads", "artifact.bin")
	tool := DownloadFileTool{Policy: SandboxPolicy{
		WorkspaceRoot:    root,
		FilesystemAccess: "workspace-write",
		NetworkAccess:    true,
	}}
	args, _ := json.Marshal(downloadFileArguments{
		URL:            server.URL + "/artifact.bin",
		Destination:    destination,
		ExpectedSHA256: hex.EncodeToString(digest[:]),
	})
	var mu sync.Mutex
	var progress []ResultPart
	ctx := WithProgressSink(context.Background(), func(part ResultPart) {
		mu.Lock()
		progress = append(progress, part)
		mu.Unlock()
	})
	result, err := tool.Execute(ctx, args)
	if err != nil || result.IsError {
		t.Fatalf("download result=%#v err=%v", result, err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("downloaded payload=%q err=%v", got, err)
	}
	if len(result.Parts) != 2 || result.Parts[0].Kind != PartToolCall || result.Parts[1].Kind != PartFile {
		t.Fatalf("result parts=%#v", result.Parts)
	}
	if result.Parts[1].Path != destination || !result.Parts[1].Created {
		t.Fatalf("file part=%#v", result.Parts[1])
	}
	mu.Lock()
	updates := append([]ResultPart(nil), progress...)
	mu.Unlock()
	if len(updates) < 2 || updates[0].Status != "running" {
		t.Fatalf("progress=%#v", updates)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".echocode-download-*.tmp")); len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestDownloadFileToolRejectsHashMismatchWithoutDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("unexpected"))
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "artifact.bin")
	tool := DownloadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true}}
	args, _ := json.Marshal(downloadFileArguments{
		URL:            server.URL,
		Destination:    destination,
		ExpectedSHA256: strings.Repeat("0", 64),
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "SHA-256 mismatch") {
		t.Fatalf("hash mismatch result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after mismatch: %v", err)
	}
}

func TestDownloadFileToolEnforcesPathAndSizeLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("payload exceeds tiny limit"))
	}))
	defer server.Close()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.bin")
	tool := DownloadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true}}
	outsideArgs, _ := json.Marshal(downloadFileArguments{URL: server.URL, Destination: outside})
	outsideResult, _ := tool.Execute(context.Background(), outsideArgs)
	if !outsideResult.IsError || !strings.Contains(outsideResult.Summary, ErrPathOutsideWorkspace.Error()) {
		t.Fatalf("outside result=%#v", outsideResult)
	}

	destination := filepath.Join(root, "too-large.bin")
	sizeArgs, _ := json.Marshal(downloadFileArguments{URL: server.URL, Destination: destination, MaxBytes: 4})
	sizeResult, _ := tool.Execute(context.Background(), sizeArgs)
	if !sizeResult.IsError || !strings.Contains(sizeResult.Summary, "limit") {
		t.Fatalf("size result=%#v", sizeResult)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("oversized destination exists: %v", err)
	}
}

func TestDownloadFileToolUsesContentDispositionAndRenamePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Disposition", `attachment; filename="release package.zip"`)
		response.Header().Set("Content-Type", "application/zip")
		_, _ = response.Write([]byte("new archive"))
	}))
	defer server.Close()

	root := t.TempDir()
	directory := filepath.Join(root, "下载 目录")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(directory, "release package.zip")
	if err := os.WriteFile(existing, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := DownloadFileTool{Policy: SandboxPolicy{
		WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true,
	}}
	args, _ := json.Marshal(downloadFileArguments{
		URL: server.URL + "/signed?id=secret", DestinationDirectory: directory, ConflictPolicy: "rename",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("directory download result=%#v err=%v", result, err)
	}
	renamed := filepath.Join(directory, "release package (1).zip")
	content, readErr := os.ReadFile(renamed)
	if readErr != nil || string(content) != "new archive" {
		t.Fatalf("renamed download=%q err=%v", content, readErr)
	}
	original, _ := os.ReadFile(existing)
	if string(original) != "existing" {
		t.Fatalf("existing file was replaced: %q", original)
	}
	if len(result.Parts) != 2 || result.Parts[1].Path != renamed || !result.Parts[1].Created {
		t.Fatalf("directory result parts=%#v", result.Parts)
	}
	if strings.Contains(result.Summary, "secret") || strings.Contains(result.Parts[0].Input, "secret") {
		t.Fatalf("signed URL leaked into result: %#v", result)
	}
}

func TestDownloadFileToolRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte("recovered"))
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "retry.bin")
	retries := 2
	tool := DownloadFileTool{
		Policy:     SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true},
		RetryDelay: time.Millisecond,
	}
	args, _ := json.Marshal(downloadFileArguments{
		URL: server.URL, Destination: destination, MaxRetries: &retries,
	})
	var mu sync.Mutex
	var progress []ResultPart
	ctx := WithProgressSink(context.Background(), func(part ResultPart) {
		mu.Lock()
		progress = append(progress, part)
		mu.Unlock()
	})
	result, err := tool.Execute(ctx, args)
	if err != nil || result.IsError || requests.Load() != 2 {
		t.Fatalf("retry result=%#v requests=%d err=%v", result, requests.Load(), err)
	}
	mu.Lock()
	updates := append([]ResultPart(nil), progress...)
	mu.Unlock()
	foundRetry := false
	for _, part := range updates {
		if part.Status == "retrying" {
			foundRetry = true
			break
		}
	}
	if !foundRetry {
		t.Fatalf("retry progress missing: %#v", updates)
	}
}

func TestDownloadFileToolRejectsHTMLMasqueradingAsAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		_, _ = response.Write([]byte("<html>download page</html>"))
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "installer.exe")
	tool := DownloadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true}}
	args, _ := json.Marshal(downloadFileArguments{URL: server.URL, Destination: destination})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "HTML page") {
		t.Fatalf("HTML result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("HTML response created destination: %v", statErr)
	}
}

func TestDownloadErrorRedactsSignedAndCredentialedURLs(t *testing.T) {
	rawURL := "https://user:password@example.com/releases/app.exe?token=temporary-secret"
	message := sanitizeDownloadError(`Get "`+rawURL+`": EOF; redirect https://other:credential@cdn.example.com/app.exe?signature=hidden`, rawURL)
	for _, secret := range []string{"user", "password", "temporary-secret", "credential", "hidden"} {
		if strings.Contains(message, secret) {
			t.Fatalf("download error leaked %q: %s", secret, message)
		}
	}
	for _, expected := range []string{"https://example.com/releases/app.exe", "https://cdn.example.com/app.exe"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("download error lost safe URL %q: %s", expected, message)
		}
	}
}

func TestDownloadFileToolCancellationRemovesPartialFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Length", "1048576")
		response.WriteHeader(http.StatusOK)
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = response.Write([]byte("partial"))
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "cancelled.bin")
	zeroRetries := 0
	tool := DownloadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write", NetworkAccess: true}}
	args, _ := json.Marshal(downloadFileArguments{URL: server.URL, Destination: destination, MaxRetries: &zeroRetries})
	ctx, cancel := context.WithCancel(context.Background())
	progressStarted := make(chan struct{}, 1)
	ctx = WithProgressSink(ctx, func(part ResultPart) {
		if part.Name == "download_file" && part.Status == "running" {
			select {
			case progressStarted <- struct{}{}:
			default:
			}
		}
	})
	done := make(chan Result, 1)
	go func() {
		result, _ := tool.Execute(ctx, args)
		done <- result
	}()
	select {
	case <-progressStarted:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("download did not start")
	}
	select {
	case result := <-done:
		if !result.IsError {
			t.Fatalf("cancelled result=%#v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled download did not stop")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled destination exists: %v", statErr)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".echocode-download-*.tmp")); len(matches) != 0 {
		t.Fatalf("cancelled download left temporary files: %v", matches)
	}
}
