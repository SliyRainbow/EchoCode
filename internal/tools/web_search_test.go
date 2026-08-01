package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchToolParsesRSSAndLimitsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "EchoCode agent" || r.URL.Query().Get("format") != "rss" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>First result</title><link>https://example.com/one</link><description><![CDATA[<b>Useful</b> first snippet]]></description></item>
			<item><title>Duplicate</title><link>https://example.com/one</link><description>duplicate</description></item>
			<item><title>Second result</title><link>https://example.com/two</link><description>Second snippet</description></item>
			<item><title>Third result</title><link>https://example.com/three</link><description>Third snippet</description></item>
		</channel></rss>`))
	}))
	defer server.Close()

	tool := WebSearchTool{
		Policy:   SandboxPolicy{NetworkAccess: true},
		Client:   server.Client(),
		Endpoint: server.URL,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"EchoCode agent","max_results":2}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(result.Parts) != 2 || result.Parts[0].Kind != PartToolCall || result.Parts[0].Name != "web_search" {
		t.Fatalf("parts=%+v", result.Parts)
	}
	if result.Parts[1].Kind != PartWebSearch || len(result.Parts[1].Sources) != 2 || result.Parts[1].Sources[0].URL != "https://example.com/one" {
		t.Fatalf("structured sources=%+v", result.Parts[1])
	}
	output := result.Parts[0].Output
	if !strings.Contains(output, "Useful first snippet") || !strings.Contains(output, "https://example.com/two") {
		t.Fatalf("output=%q", output)
	}
	if strings.Contains(output, "example.com/three") || strings.Count(output, "example.com/one") != 1 {
		t.Fatalf("limit/dedup failed: %q", output)
	}
}

func TestWebSearchToolParsesBraveHTMLResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "宁波 台风预警" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<div data-type="web">
				<a href="https://typhoon.example/current"><div class="title search-snippet-title">实时台风消息</div></a>
				<div class="generic-snippet"><div class="content">浙江沿海台风实时路径</div></div>
			</div>
			<div data-type="web">
				<a href="https://weather.example/ningbo"><div class="title">宁波天气</div></a>
				<div class="generic-snippet">宁波气象台预警</div>
			</div>
		</body></html>`))
	}))
	defer server.Close()

	tool := WebSearchTool{
		Policy:   SandboxPolicy{NetworkAccess: true},
		Client:   server.Client(),
		Endpoint: server.URL,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"宁波 台风预警","max_results":2}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(result.Parts) != 2 || len(result.Parts[1].Sources) != 2 {
		t.Fatalf("parts=%+v", result.Parts)
	}
	first := result.Parts[1].Sources[0]
	if first.Title != "实时台风消息" || first.URL != "https://typhoon.example/current" || !strings.Contains(first.Snippet, "实时路径") {
		t.Fatalf("first source=%+v", first)
	}
}

func TestWebSearchToolHonorsNetworkPolicy(t *testing.T) {
	tool := WebSearchTool{Policy: SandboxPolicy{NetworkAccess: false}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"blocked"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Parts) != 1 || result.Parts[0].Status != "error" {
		t.Fatalf("network-disabled result=%+v", result)
	}
}
