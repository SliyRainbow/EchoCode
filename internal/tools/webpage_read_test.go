package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeWebpageBrowserRenderer struct {
	snapshot string
	err      error
	url      string
}

func (f *fakeWebpageBrowserRenderer) ReadURLSnapshot(_ context.Context, targetURL string) (string, error) {
	f.url = targetURL
	return f.snapshot, f.err
}

func TestReadWebpageToolExtractsActualContentAndLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<title>Pitch Tool</title><meta name="description" content="Change song pitch online">
			<script>window.secret = "not readable content"</script>
		</head><body><nav>Navigation noise</nav><main>
			<h1>Online pitch shifter</h1><p>Raise or lower a song by semitones without changing tempo.</p>
			<a href="/features/export">Export audio</a><a href="https://example.org/help#faq">Help</a>
		</main></body></html>`))
	}))
	defer server.Close()

	tool := ReadWebpageTool{Policy: SandboxPolicy{NetworkAccess: true}, Client: server.Client()}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+server.URL+`","max_chars":4000}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Name != "read_webpage" {
		t.Fatalf("parts=%+v", result.Parts)
	}
	output := result.Parts[0].Output
	for _, expected := range []string{"Title: Pitch Tool", "Online pitch shifter", "without changing tempo", server.URL + "/features/export", "https://example.org/help"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
	for _, unwanted := range []string{"window.secret", "Navigation noise"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output contains hidden noise %q:\n%s", unwanted, output)
		}
	}
}

func TestReadWebpageToolRendersJavaScriptOnlyPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script>document.body.textContent = "loaded"</script></body></html>`))
	}))
	defer server.Close()

	snapshot, err := json.Marshal(map[string]any{
		"title": "豆包",
		"url":   server.URL + "/chat",
		"text":  "这是 JavaScript 渲染后的真实页面正文。",
		"elements": []map[string]string{
			{"name": "帮助中心", "href": "/help"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer := &fakeWebpageBrowserRenderer{snapshot: string(snapshot)}
	tool := ReadWebpageTool{Policy: SandboxPolicy{NetworkAccess: true}, Client: server.Client(), Browser: renderer}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if renderer.url != server.URL {
		t.Fatalf("renderer URL = %q, want %q", renderer.url, server.URL)
	}
	output := result.Parts[0].Output
	for _, expected := range []string{"EchoCode managed browser snapshot", "Title: 豆包", "JavaScript 渲染后的真实页面正文", server.URL + "/help"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
}

func TestReadWebpageToolReportsUnavailableBrowserForJavaScriptPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script>document.body.textContent = "loaded"</script></body></html>`))
	}))
	defer server.Close()

	tool := ReadWebpageTool{Policy: SandboxPolicy{NetworkAccess: true}, Client: server.Client()}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`))
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "启用内置浏览器") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReadWebpageToolReportsBrowserRenderingFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()

	renderer := &fakeWebpageBrowserRenderer{err: errors.New("browser unavailable")}
	tool := ReadWebpageTool{Policy: SandboxPolicy{NetworkAccess: true}, Client: server.Client(), Browser: renderer}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+server.URL+`"}`))
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "browser unavailable") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReadWebpageToolHonorsNetworkPolicy(t *testing.T) {
	tool := ReadWebpageTool{Policy: SandboxPolicy{NetworkAccess: false}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil || !result.IsError || result.Parts[0].Status != "error" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
