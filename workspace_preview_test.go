package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePreviewServesHTMLAndRelativeAssets(t *testing.T) {
	root := t.TempDir()
	pageDir := filepath.Join(root, "site")
	if err := os.MkdirAll(filepath.Join(pageDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(pageDir, "index.html")
	if err := os.WriteFile(page, []byte(`<link rel="stylesheet" href="assets/site.css"><h1>EchoCode</h1>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "assets", "site.css"), []byte("h1 { color: green; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newWorkspacePreviewServer()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	preview, err := server.Preview(root, page)
	if err != nil {
		t.Fatal(err)
	}
	pageInfo, statErr := os.Stat(page)
	previewInfo, previewStatErr := os.Stat(preview.Path)
	if preview.Name != "index.html" || statErr != nil || previewStatErr != nil || !os.SameFile(pageInfo, previewInfo) {
		t.Fatalf("preview = %+v", preview)
	}
	if body := getPreviewBody(t, preview.URL); !strings.Contains(body, "EchoCode") {
		t.Fatalf("HTML body = %q", body)
	}
	pageURL, err := url.Parse(preview.URL)
	if err != nil {
		t.Fatal(err)
	}
	assetURL := pageURL.ResolveReference(&url.URL{Path: "assets/site.css"})
	if body := getPreviewBody(t, assetURL.String()); !strings.Contains(body, "color: green") {
		t.Fatalf("asset body = %q", body)
	}
}

func TestWorkspacePreviewInvalidatesOldToken(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.html")
	secondPath := filepath.Join(root, "second.html")
	if err := os.WriteFile(firstPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := newWorkspacePreviewServer()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	first, err := server.Preview(root, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.Preview(root, secondPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("old preview status = %d, want 404", response.StatusCode)
	}
	if body := getPreviewBody(t, second.URL); body != "second" {
		t.Fatalf("second body = %q", body)
	}
}

func TestWorkspacePreviewRejectsFileOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := newWorkspacePreviewServer()
	if _, err := server.Preview(root, outside); err == nil {
		t.Fatal("outside file should not be previewable")
	}
}

func getPreviewBody(t *testing.T, target string) string {
	t.Helper()
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", target, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
