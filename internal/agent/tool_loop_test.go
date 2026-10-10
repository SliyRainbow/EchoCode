package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type scriptedCompletion struct {
	result protocol.CompletionResult
	err    error
}

type scriptedToolCaller struct {
	steps []scriptedCompletion
	next  int
}

type failingBrowserTool struct{ calls *int }

type staticRepositoryTool struct{}

type staticSearchEvidenceTool struct{}

type staticWebpageTool struct{}

type longTaskStepTool struct{ calls *int }

type cycleProbeTool struct {
	calls    *int
	changing bool
}

type streamingProgressTool struct{}

type imageAttachmentTool struct{}

type deterministicFailureTool struct{ calls *int }

type structuredResultErrorTool struct{}

type namedSuccessTool struct {
	name  string
	calls *int
}

func (t deterministicFailureTool) Name() string { return "run_command" }
func (t deterministicFailureTool) Description() string {
	return "fails deterministically for retry tests"
}
func (t deterministicFailureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t deterministicFailureTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	(*t.calls)++
	exitCode := 1
	return tools.Result{
		Summary: "python exited with code 1",
		IsError: true,
		Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: "run_command", Status: "error",
			Stderr: "SyntaxError: unterminated string literal", Output: "SyntaxError: unterminated string literal", ExitCode: &exitCode,
		}},
	}, nil
}

func (structuredResultErrorTool) Name() string { return "structured_result_error" }
func (structuredResultErrorTool) Description() string {
	return "returns usable evidence together with an execution error"
}
func (structuredResultErrorTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (structuredResultErrorTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{
		Summary: "partial inspection result",
		Parts: []tools.ResultPart{
			{Kind: tools.PartText, Text: "configuration file was parsed"},
			{Kind: tools.PartToolCall, Name: "structured_result_error", Status: "ok", Stdout: "parsed 3 entries"},
		},
	}, errors.New("post-parse validation failed")
}

func (t namedSuccessTool) Name() string        { return t.name }
func (t namedSuccessTool) Description() string { return "succeeds as an alternative strategy" }
func (t namedSuccessTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t namedSuccessTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	(*t.calls)++
	return tools.Result{Summary: t.name + " completed"}, nil
}

func (streamingProgressTool) Name() string        { return "stream_probe" }
func (streamingProgressTool) Description() string { return "streams structured output for a test" }
func (streamingProgressTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (streamingProgressTool) Execute(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
	tools.EmitProgress(ctx, tools.ResultPart{Kind: tools.PartToolCall, Status: "waiting", Output: "waiting for probe", WorkingDirectory: "workspace"})
	tools.EmitProgress(ctx, tools.ResultPart{Kind: tools.PartToolCall, Status: "running", Stdout: "first", Output: "first", WorkingDirectory: "workspace"})
	tools.EmitProgress(ctx, tools.ResultPart{Kind: tools.PartToolCall, Status: "running", Stdout: "first\nsecond", Output: "first\nsecond", WorkingDirectory: "workspace"})
	exitCode := 0
	return tools.Result{Summary: "probe complete", Parts: []tools.ResultPart{{
		Kind: tools.PartToolCall, Status: "ok", Stdout: "first\nsecond", Output: "first\nsecond", WorkingDirectory: "workspace", ExitCode: &exitCode,
	}}}, nil
}

func (imageAttachmentTool) Name() string        { return "capture_image" }
func (imageAttachmentTool) Description() string { return "returns a screenshot for visual inspection" }
func (imageAttachmentTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (imageAttachmentTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{
		Summary: "screenshot captured",
		Attachments: []tools.Attachment{{
			Name: "capture.png", MIMEType: "image/png", Data: "aGVsbG8=",
		}},
	}, nil
}

func (t longTaskStepTool) Name() string        { return "long_task_step" }
func (t longTaskStepTool) Description() string { return "records one step in a long-running test task" }
func (t longTaskStepTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t longTaskStepTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	(*t.calls)++
	return tools.Result{Summary: "step completed"}, nil
}

func (t cycleProbeTool) Name() string        { return "cycle_probe" }
func (t cycleProbeTool) Description() string { return "returns a stable or changing probe result" }
func (t cycleProbeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t cycleProbeTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	(*t.calls)++
	summary := "unchanged"
	if t.changing {
		summary = fmt.Sprintf("progress-%d", *t.calls)
	}
	return tools.Result{Summary: summary}, nil
}

func (staticRepositoryTool) Name() string        { return "read_repository" }
func (staticRepositoryTool) Description() string { return "read a test repository" }
func (staticRepositoryTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (staticRepositoryTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	output := "Repository: SliyRainbow/EchoCode\nURL: https://github.com/SliyRainbow/EchoCode\nRef: main\nCommit: abc123\n\nREADME:\n# EchoCode\n\nRepository tree:\nfrontend/\ninternal/"
	return tools.Result{
		Summary: "已读取 GitHub 仓库 SliyRainbow/EchoCode（main）",
		Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: "read_repository", Status: "ok",
			Input: "https://github.com/SliyRainbow/EchoCode", Output: output,
		}},
	}, nil
}

func (staticSearchEvidenceTool) Name() string        { return "web_search" }
func (staticSearchEvidenceTool) Description() string { return "returns discovery-only search evidence" }
func (staticSearchEvidenceTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (staticSearchEvidenceTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{
		Summary: "found official documentation candidate: CCSwitch official repository https://github.com/example/ccswitch",
		Parts: []tools.ResultPart{{
			Kind:  tools.PartWebSearch,
			Query: "CCSwitch official repository",
			Sources: []tools.SearchSource{{
				Title: "CCSwitch official repository",
				URL:   "https://github.com/example/ccswitch",
			}},
		}},
	}, nil
}

func (staticWebpageTool) Name() string        { return "read_webpage" }
func (staticWebpageTool) Description() string { return "reads verified official documentation" }
func (staticWebpageTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (staticWebpageTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{
		Summary: "official documentation read",
		Parts: []tools.ResultPart{{
			Kind:   tools.PartToolCall,
			Name:   "read_webpage",
			Status: "ok",
			Input:  "https://github.com/example/ccswitch",
			Output: "Official configuration path: %APPDATA%/CCSwitch/config.json",
		}},
	}, nil
}

func (t failingBrowserTool) Name() string        { return "browser" }
func (t failingBrowserTool) Description() string { return "test browser" }
func (t failingBrowserTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t failingBrowserTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	(*t.calls)++
	summary := "browser open 执行失败: 内置浏览器启动失败。EchoCode 已尝试备用浏览器和隔离配置目录。"
	return tools.Result{
		Summary: summary,
		IsError: true,
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: "browser", Status: "error", Output: summary}},
	}, nil
}

func (c *scriptedToolCaller) Complete(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
	if c.next >= len(c.steps) {
		return protocol.CompletionResult{}, errors.New("unexpected completion call")
	}
	step := c.steps[c.next]
	c.next++
	return step.result, step.err
}

func newNativeToolLoopService(t *testing.T) (*Service, string) {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "fixture.txt"), []byte("hello from fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	return svc, workspace
}

func TestAnthropicStreamingToolLoopExecutesAndReturnsToolResult(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requestCount.Add(1) {
		case 1:
			if !strings.Contains(string(body), `"tools"`) {
				t.Errorf("first Anthropic request does not declare tools: %s", body)
			}
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signed\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_read\",\"name\":\"read_file\",\"input\":{\"path\":\"fixture.txt\"}}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
		case 2:
			payload := string(body)
			for _, expected := range []string{`"type":"thinking"`, `"thinking":"plan"`, `"signature":"signed"`, `"type":"tool_result"`, `"tool_use_id":"toolu_read"`, "hello from fixture"} {
				if !strings.Contains(payload, expected) {
					t.Errorf("second Anthropic request missing %q: %s", expected, payload)
				}
			}
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"anthropic done\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	provider := protocol.AnthropicProvider{BaseURL: server.URL, APIKey: "test-key"}
	outcome, err := svc.runStreamingToolLoop(context.Background(), provider, svc.buildToolRegistry(), protocol.ChatRequest{
		Model:    "claude-test",
		Messages: []protocol.Message{{Role: "user", Content: "read fixture.txt"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "anthropic done" || requestCount.Load() != 2 {
		t.Fatalf("outcome = %#v, requests = %d", outcome, requestCount.Load())
	}
}

func TestGeminiStreamingToolLoopExecutesAndReturnsFunctionResponse(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requestCount.Add(1) {
		case 1:
			if !strings.Contains(string(body), `"functionDeclarations"`) {
				t.Errorf("first Gemini request does not declare tools: %s", body)
			}
			_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"plan\",\"thought\":true,\"thoughtSignature\":\"thought-signed\"},{\"functionCall\":{\"id\":\"gemini-read\",\"name\":\"read_file\",\"args\":{\"path\":\"fixture.txt\"}},\"thoughtSignature\":\"tool-signed\"}]}}]}\n\n"))
		case 2:
			payload := string(body)
			for _, expected := range []string{`"thoughtSignature":"thought-signed"`, `"thoughtSignature":"tool-signed"`, `"functionResponse"`, `"id":"gemini-read"`, "hello from fixture"} {
				if !strings.Contains(payload, expected) {
					t.Errorf("second Gemini request missing %q: %s", expected, payload)
				}
			}
			_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"gemini done\"}]}}]}\n\n"))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	provider := protocol.GeminiProvider{BaseURL: server.URL, APIKey: "test-key"}
	outcome, err := svc.runStreamingToolLoop(context.Background(), provider, svc.buildToolRegistry(), protocol.ChatRequest{
		Model:    "gemini-test",
		Messages: []protocol.Message{{Role: "user", Content: "read fixture.txt"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "gemini done" || requestCount.Load() != 2 {
		t.Fatalf("outcome = %#v, requests = %d", outcome, requestCount.Load())
	}
}

func TestDeepSeekThinkingToolLoopReturnsReasoningContent(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch requestCount.Add(1) {
		case 1:
			for _, expected := range []string{`"thinking":{"type":"enabled"}`, `"reasoning_effort":"high"`, `"tools"`} {
				if !strings.Contains(string(body), expected) {
					t.Errorf("first DeepSeek request missing %q: %s", expected, body)
				}
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"checking","reasoning_content":"signed plan","tool_calls":[{"id":"deepseek-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"fixture.txt\"}"}}]}}]}`))
		case 2:
			payload := string(body)
			for _, expected := range []string{`"reasoning_content":"signed plan"`, `"tool_call_id":"deepseek-read"`, "hello from fixture"} {
				if !strings.Contains(payload, expected) {
					t.Errorf("second DeepSeek request missing %q: %s", expected, payload)
				}
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"deepseek done","reasoning_content":"verified result"}}]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	provider := protocol.DeepSeekProvider{BaseURL: server.URL, APIKey: "test-key"}
	outcome, err := svc.runToolLoop(context.Background(), provider, svc.buildToolRegistry(), protocol.ChatRequest{
		Model:          "deepseek-v4-pro",
		ReasoningLevel: "high",
		Messages:       []protocol.Message{{Role: "user", Content: "read fixture.txt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "deepseek done" || requestCount.Load() != 2 {
		t.Fatalf("outcome = %#v, requests = %d", outcome, requestCount.Load())
	}
	if outcome.Reasoning != "signed plan\n\nverified result" {
		t.Fatalf("reasoning from earlier tool rounds was lost: %q", outcome.Reasoning)
	}
}

func TestWebSearchToolLoopFeedsSourcesIntoFinalCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>Ningbo warning</title><link>https://weather.example/warning</link><description>Typhoon warning details</description></item>
			<item><title>Forecast</title><link>https://weather.example/forecast</link><description>Latest forecast</description></item>
		</channel></rss>`))
	}))
	defer server.Close()

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(tools.WebSearchTool{
		Policy:   tools.SandboxPolicy{NetworkAccess: true},
		Client:   server.Client(),
		Endpoint: server.URL,
	})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID:   "search-1",
				Type: "function",
				Function: protocol.ToolCallFunction{
					Name:      "web_search",
					Arguments: json.RawMessage(`{"query":"宁波 台风 预警","max_results":2}`),
				},
			}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		for _, expected := range []string{"Ningbo warning", "https://weather.example/warning", "Typhoon warning details"} {
			if !strings.Contains(last.Content, expected) {
				t.Errorf("tool feedback missing %q: %s", expected, last.Content)
			}
		}
		return protocol.CompletionResult{Content: "宁波预警信息已整理：Ningbo warning。"}, nil
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model:    "test-model",
		Messages: []protocol.Message{{Role: "user", Content: "搜索宁波台风预警"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completionCalls != 2 {
		t.Fatalf("calls=%d outcome=%+v", completionCalls, outcome)
	}
	if outcome.Content != "宁波预警信息已整理：Ningbo warning。" {
		t.Fatalf("host rewrote the model final answer: %q", outcome.Content)
	}
	if strings.Contains(outcome.Content, "https://weather.example/warning") || strings.Contains(outcome.Content, "https://weather.example/forecast") {
		t.Fatalf("host appended search sources to the model final answer: %s", outcome.Content)
	}
	foundSources := false
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartWebSearch && len(part.Sources) == 2 {
			foundSources = true
		}
	}
	if !foundSources {
		t.Fatalf("structured search sources missing: %+v", outcome.Parts)
	}
}

func TestToolLoopReusesIdenticalWebSearchAndMergesSources(t *testing.T) {
	searchRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		searchRequests++
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>宁波天气预警</title><link>https://weather.example/warning</link><description>宁波市气象台发布预警。</description></item>
		</channel></rss>`))
	}))
	defer server.Close()

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(tools.WebSearchTool{
		Policy: tools.SandboxPolicy{NetworkAccess: true}, Client: server.Client(), Endpoint: server.URL,
	})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "web_search", Arguments: json.RawMessage(`{"query":"宁波 今天 台风预警 2026年 宁波气象台"}`),
				},
			}}}, nil
		case 2:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-2", Type: "function", Function: protocol.ToolCallFunction{
					Name: "web_search", Arguments: json.RawMessage(`{"query":"  宁波  今天 台风预警 2026年 宁波气象台  ","max_results":6}`),
				},
			}}}, nil
		default:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "复用已有 1 条来源") {
				t.Fatalf("duplicate search feedback = %q", request.Messages[len(request.Messages)-1].Content)
			}
			return protocol.CompletionResult{Content: "已根据来源整理。"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "查询宁波台风预警"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if searchRequests != 1 || completionCalls != 3 {
		t.Fatalf("searchRequests=%d completionCalls=%d", searchRequests, completionCalls)
	}
	searchParts := 0
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartToolCall && part.Name == "web_search" && part.Status != "error" {
			t.Fatalf("successful search tool card should be folded into sources: %+v", outcome.Parts)
		}
		if part.Kind == tools.PartWebSearch {
			searchParts++
			if len(part.Sources) != 1 || part.Sources[0].URL != "https://weather.example/warning" {
				t.Fatalf("search part = %+v", part)
			}
		}
	}
	if searchParts != 1 {
		t.Fatalf("search parts=%d outcome=%+v", searchParts, outcome.Parts)
	}
}

func TestToolLoopKeepsUsageForEveryModelRequest(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	calls := 0
	registry := tools.NewRegistry(longTaskStepTool{calls: &calls})
	completions := 0
	complete := func(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
		completions++
		if completions == 1 {
			return protocol.CompletionResult{
				Usage: &protocol.TokenUsage{PromptTokens: 100, CompletionTokens: 5},
				ToolCalls: []protocol.ToolCall{{
					ID: "step-1", Type: "function", Function: protocol.ToolCallFunction{Name: "long_task_step", Arguments: json.RawMessage(`{}`)},
				}},
			}, nil
		}
		return protocol.CompletionResult{
			Content: "completed",
			Usage:   &protocol.TokenUsage{PromptTokens: 140, CompletionTokens: 8},
		}, nil
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "run"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.UsageSamples) != 2 {
		t.Fatalf("usage samples = %#v", outcome.UsageSamples)
	}
	if outcome.UsageSamples[0].PromptTokens != 100 || outcome.UsageSamples[1].PromptTokens != 140 {
		t.Fatalf("usage samples = %#v", outcome.UsageSamples)
	}
}

func TestToolLoopDoesNotRestartUnavailableBrowser(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "never"
	browserCalls := 0
	registry := tools.NewRegistry(failingBrowserTool{calls: &browserCalls})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls <= 2 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: fmt.Sprintf("browser-%d", completionCalls), Type: "function", Function: protocol.ToolCallFunction{
					Name: "browser", Arguments: json.RawMessage(fmt.Sprintf(`{"action":"open","url":"https://example.com/%d"}`, completionCalls)),
				},
			}}}, nil
		}
		if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "已跳过重复启动") {
			t.Fatalf("browser short-circuit feedback = %q", request.Messages[len(request.Messages)-1].Content)
		}
		feedback := request.Messages[len(request.Messages)-1].Content
		if !strings.Contains(feedback, "明确说明页面尚无法核验") || strings.Contains(feedback, "已有搜索来源完成回答") {
			t.Fatalf("browser short-circuit feedback = %q", feedback)
		}
		return protocol.CompletionResult{Content: "浏览器不可用，页面暂时无法核验。"}, nil
	}
	_, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "打开网页"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if browserCalls != 1 {
		t.Fatalf("browser executed %d times, want 1", browserCalls)
	}
}

func TestToolLoopAllowsModelSelectedBrowserAfterSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>Ningbo warning</title><link>https://weather.example/warning</link><description>Typhoon warning details</description></item>
		</channel></rss>`))
	}))
	defer server.Close()

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "never"
	browserCalls := 0
	registry := tools.NewRegistry(
		tools.WebSearchTool{Policy: tools.SandboxPolicy{NetworkAccess: true}, Client: server.Client(), Endpoint: server.URL},
		failingBrowserTool{calls: &browserCalls},
	)
	completionCalls := 0
	var events []ChatStreamEvent
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "web_search", Arguments: json.RawMessage(`{"query":"宁波 台风预警","max_results":1}`),
				},
			}}}, nil
		case 2:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "browser-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "browser", Arguments: json.RawMessage(`{"action":"open","url":"https://weather.example/warning"}`),
				},
			}}}, nil
		default:
			feedback := request.Messages[len(request.Messages)-1].Content
			if !strings.Contains(feedback, "浏览器") {
				t.Fatalf("browser execution feedback missing: %s", feedback)
			}
			return protocol.CompletionResult{Content: "宁波预警信息已整理。\n\n来源：\n- Ningbo warning\n  https://weather.example/warning"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "搜索宁波台风预警并附来源链接"}},
	}, complete, func(event ChatStreamEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if browserCalls != 1 {
		t.Fatalf("browser executed %d times, want 1", browserCalls)
	}
	browserEvent := false
	for _, event := range events {
		if event.Type == "tool" && event.ToolName == "browser" {
			browserEvent = true
		}
	}
	if !browserEvent {
		t.Fatalf("model-selected browser call was missing from activity events: %+v", events)
	}
	for _, expected := range []string{"Ningbo warning", "https://weather.example/warning"} {
		if !strings.Contains(outcome.Content, expected) {
			t.Fatalf("final answer missing visible source detail %q: %s", expected, outcome.Content)
		}
	}
	browserPart := false
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartToolCall && part.Name == "browser" {
			browserPart = true
		}
	}
	if !browserPart {
		t.Fatalf("model-selected browser call was missing from result parts: %+v", outcome.Parts)
	}
}

func TestToolLoopGuardLeavesBrowserSelectionToModel(t *testing.T) {
	guard := toolLoopGuard{}
	call := protocol.ToolCall{
		ID: "browser-open",
		Function: protocol.ToolCallFunction{
			Name:      "browser",
			Arguments: json.RawMessage(`{"action":"open","url":"https://example.com"}`),
		},
	}
	if _, _, guarded, _ := guard.before(call); guarded {
		t.Fatal("the host must not keyword-route a browser call selected by the model")
	}
}

func TestWebSearchRequestSignatureUsesAllEffectiveArguments(t *testing.T) {
	base := webSearchRequestSignature(json.RawMessage(`{"query":"  宁波 台风预警  "}`))
	if base == "" {
		t.Fatal("default search signature is empty")
	}
	if same := webSearchRequestSignature(json.RawMessage(`{"max_results":6,"query":"宁波   台风预警"}`)); same != base {
		t.Fatalf("equivalent search requests must share a signature: %q vs %q", base, same)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"query":"宁波 台风预警","max_results":7}`),
		json.RawMessage(`{"query":"site:nmc.cn 宁波 台风预警","max_results":6}`),
		json.RawMessage(`{"query":"Claude 今天版本","max_results":6}`),
	} {
		if signature := webSearchRequestSignature(raw); signature == base {
			t.Fatalf("distinct model-selected search was suppressed: %s", raw)
		}
	}
}

func TestWebSearchToolLoopRetriesFinalSynthesisWithoutRepeatingSearch(t *testing.T) {
	searchRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		searchRequests++
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>Repository</title><link>https://github.com/example/project</link><description>Source repository</description></item>
		</channel></rss>`))
	}))
	defer server.Close()

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(tools.WebSearchTool{
		Policy: tools.SandboxPolicy{NetworkAccess: true}, Client: server.Client(), Endpoint: server.URL,
	})
	completionCalls := 0
	var events []ChatStreamEvent
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-retry", Type: "function", Function: protocol.ToolCallFunction{
					Name: "web_search", Arguments: json.RawMessage(`{"query":"example project","max_results":1}`),
				},
			}}}, nil
		case 2:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "https://github.com/example/project") {
				t.Fatal("retry request lost the completed search result")
			}
			return protocol.CompletionResult{}, io.ErrUnexpectedEOF
		default:
			return protocol.CompletionResult{Content: "search result synthesized"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "inspect example project"}},
	}, complete, func(event ChatStreamEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outcome.Content, "search result synthesized") || strings.Contains(outcome.Content, "https://github.com/example/project") || completionCalls != 3 || searchRequests != 1 {
		t.Fatalf("content=%q completionCalls=%d searchRequests=%d", outcome.Content, completionCalls, searchRequests)
	}
	foundStructuredSource := false
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartWebSearch && len(part.Sources) == 1 && part.Sources[0].URL == "https://github.com/example/project" {
			foundStructuredSource = true
			break
		}
	}
	if !foundStructuredSource {
		t.Fatalf("search source should remain available in structured activity: %+v", outcome.Parts)
	}
	foundRetryStatus := false
	for _, event := range events {
		if event.Type == "status" && strings.Contains(event.Message, "继续整理工具结果") {
			foundRetryStatus = true
			break
		}
	}
	if !foundRetryStatus {
		t.Fatalf("retry status event missing: %+v", events)
	}
}

func TestWebSearchEvidenceCannotCompleteFailedTurn(t *testing.T) {
	outcome := toolLoopOutcome{Parts: []tools.ResultPart{{
		Kind:  tools.PartWebSearch,
		Query: "宁波 台风预警",
		Sources: []tools.SearchSource{
			{Title: "宁波天气预警", URL: "https://weather.example/warning", Snippet: "宁波市气象台发布最新预警。"},
			{Title: "实时台风消息", URL: "https://weather.example/typhoon", Snippet: "台风路径与防御信息。"},
		},
	}}}
	if !hasWebSearchSources(outcome.Parts) {
		t.Fatal("structured search sources should remain available as activity evidence")
	}
	content := partialToolFailureContent(outcome)
	for _, expected := range []string{"工具结果已经保留", "尚未形成可用结论"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("failure message missing %q: %s", expected, content)
		}
	}
	for _, forbidden := range []string{"网络搜索已完成", "宁波天气预警", "宁波市气象台发布最新预警", "https://weather.example/warning"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("search evidence was promoted into a failed turn answer via %q: %s", forbidden, content)
		}
	}
}

func TestToolLoopContinuesInvestigationAfterEmptySearchCompletion(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(staticSearchEvidenceTool{}, staticWebpageTool{})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "web_search", Arguments: json.RawMessage(`{"query":"CCSwitch official repository"}`),
				},
			}}}, nil
		case 2:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "https://github.com/example/ccswitch") {
				t.Fatalf("search evidence was not returned to the model: %+v", request.Messages)
			}
			return protocol.CompletionResult{}, nil
		case 3:
			last := request.Messages[len(request.Messages)-1]
			if last.InternalKind != toolResultRecoveryKind ||
				!strings.Contains(last.Content, "Continue the task autonomously in this same turn") ||
				!strings.Contains(last.Content, "call a substantive tool now") {
				t.Fatalf("first empty completion did not request further investigation: %+v", last)
			}
			if len(request.Tools) == 0 || request.ToolChoice == "none" {
				t.Fatalf("tools were disabled before the model could verify the source: %+v", request)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "page-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "read_webpage", Arguments: json.RawMessage(`{"url":"https://github.com/example/ccswitch"}`),
				},
			}}}, nil
		default:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Official configuration path") {
				t.Fatalf("verified webpage evidence was not returned to the model: %+v", request.Messages)
			}
			return protocol.CompletionResult{Content: "已读取官方资料，接下来需要检查本机配置。"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "检查我的 CCSwitch 配置"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completionCalls != 4 || !strings.Contains(outcome.Content, "已读取官方资料") || !hasSuccessfulToolEvidence(outcome.Parts, "read_webpage") {
		t.Fatalf("calls=%d outcome=%+v", completionCalls, outcome)
	}
}

func TestToolLoopKeepsToolsAvailableAfterRepeatedEmptyCompletion(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(staticSearchEvidenceTool{}, staticWebpageTool{})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-1", Type: "function", Function: protocol.ToolCallFunction{Name: "web_search", Arguments: json.RawMessage(`{}`)},
			}}}, nil
		case 2:
			return protocol.CompletionResult{}, nil
		case 3:
			if len(request.Tools) == 0 || request.ToolChoice == "none" {
				t.Fatalf("investigation tools were disabled on the first recovery request: %+v", request)
			}
			return protocol.CompletionResult{}, nil
		case 4:
			last := request.Messages[len(request.Messages)-1]
			if request.ToolChoice != "required" || len(request.Tools) == 0 || last.InternalKind != toolResultRecoveryKind || !strings.Contains(last.Content, "call a substantive tool now") {
				t.Fatalf("repeated empty completion did not require a real tool action: request=%+v last=%+v", request, last)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "page-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "read_webpage", Arguments: json.RawMessage(`{"url":"https://github.com/example/ccswitch"}`),
				},
			}}}, nil
		default:
			return protocol.CompletionResult{Content: "已读取实际页面并完成核验。"}, nil
		}
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "检查我的 CCSwitch 配置"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completionCalls != 5 || !strings.Contains(outcome.Content, "完成核验") || !hasSuccessfulToolEvidence(outcome.Parts, "read_webpage") {
		t.Fatalf("calls=%d outcome=%+v", completionCalls, outcome)
	}
}

func TestToolLoopFailsWhenFinalToolResultSynthesisIsEmpty(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(staticSearchEvidenceTool{})
	completionCalls := 0
	complete := func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "search-1", Type: "function", Function: protocol.ToolCallFunction{Name: "web_search", Arguments: json.RawMessage(`{}`)},
			}}}, nil
		}
		return protocol.CompletionResult{}, nil
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "检查我的 CCSwitch 配置"}},
	}, complete, nil)
	if !errors.Is(err, errEmptyToolResultSynthesis) {
		t.Fatalf("error=%v, want %v", err, errEmptyToolResultSynthesis)
	}
	if completionCalls != 1+maxEmptyCompletionRecoveries || !hasWebSearchSources(outcome.Parts) {
		t.Fatalf("calls=%d outcome=%+v", completionCalls, outcome)
	}
	if strings.Contains(outcome.Content, "CCSwitch official repository") || strings.Contains(outcome.Content, "https://github.com/example/ccswitch") {
		t.Fatalf("raw search evidence became a successful answer: %q", outcome.Content)
	}
}

func TestWebSearchFallbackIncludesEverySource(t *testing.T) {
	sources := make([]tools.SearchSource, 0, 7)
	for index := 1; index <= 7; index++ {
		sources = append(sources, tools.SearchSource{
			Title: fmt.Sprintf("Source %d", index),
			URL:   fmt.Sprintf("https://example.com/source-%d", index),
		})
	}
	content := webSearchFallbackContent(tools.ResultPart{Kind: tools.PartWebSearch, Sources: sources})
	if !strings.Contains(content, "本轮未完成最终分析") || !strings.Contains(content, "原始网络搜索记录") {
		t.Fatalf("legacy migration must mark search-only content as incomplete: %s", content)
	}
	if !strings.Contains(content, "Source 7") || !strings.Contains(content, "https://example.com/source-7") {
		t.Fatalf("fallback omitted later sources: %s", content)
	}
}

func TestRepositoryReadFailureKeepsEvidenceWithoutHostSynthesizingAnswer(t *testing.T) {
	part := tools.ResultPart{
		Kind:   tools.PartToolCall,
		Name:   "read_repository",
		Status: "ok",
		Input:  "https://github.com/SliyRainbow/EchoCode",
		Output: strings.Join([]string{
			"Repository: SliyRainbow/EchoCode",
			"URL: https://github.com/SliyRainbow/EchoCode",
			"Ref: main",
			"Commit: abc123",
			"Language: Go",
			"",
			"README:",
			"# EchoCode",
			"A coding agent workbench.",
			"",
			"Repository tree:",
			"frontend/",
			"internal/",
			"go.mod (123 bytes)",
		}, "\n"),
	}
	outcome := toolLoopOutcome{Parts: []tools.ResultPart{part}}
	if !hasSuccessfulToolEvidence(outcome.Parts, "read_repository") {
		t.Fatal("a successful repository read should remain usable when final synthesis fails")
	}
	content := partialToolFailureContent(outcome)
	if !strings.Contains(content, "工具结果已经保留") {
		t.Fatalf("repository failure did not explain retained evidence: %s", content)
	}
	for _, forbidden := range []string{"GitHub 仓库读取已完成", "Commit: abc123", "A coding agent workbench.", "frontend/"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("host synthesized repository evidence %q into business content: %s", forbidden, content)
		}
	}
}

func TestRepositoryReadToolLoopKeepsResultAfterFinalSynthesisEOF(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(staticRepositoryTool{})
	completionCalls := 0
	complete := func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "repo-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "read_repository", Arguments: json.RawMessage(`{"url":"https://github.com/SliyRainbow/EchoCode"}`),
				},
			}}}, nil
		}
		if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Commit: abc123") {
			t.Fatalf("repository result was not preserved for synthesis: %s", request.Messages[len(request.Messages)-1].Content)
		}
		return protocol.CompletionResult{}, io.ErrUnexpectedEOF
	}

	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "读取 EchoCode 仓库"}},
	}, complete, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want unexpected EOF", err)
	}
	if completionCalls != 2+postToolCompletionRetries {
		t.Fatalf("completion calls = %d, want %d", completionCalls, 2+postToolCompletionRetries)
	}
	content := partialToolFailureContent(outcome)
	if !hasSuccessfulToolEvidence(outcome.Parts, "read_repository") || !strings.Contains(content, "工具结果已经保留") || strings.Contains(content, "Commit: abc123") || strings.Contains(content, "frontend/") {
		t.Fatalf("repository partial result was lost: outcome=%+v content=%s", outcome, content)
	}
}

func hasSuccessfulToolEvidence(parts []tools.ResultPart, name string) bool {
	for _, part := range parts {
		if part.Kind == tools.PartToolCall && part.Name == name && part.Status != "error" && strings.TrimSpace(part.Output) != "" {
			return true
		}
	}
	return false
}

func TestToolLoopEmitsAndPersistsTaskProgressWithDiffStats(t *testing.T) {
	workspace := t.TempDir()
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	registry := tools.NewRegistry(
		tools.UpdatePlanTool{},
		tools.WriteFileTool{Policy: svc.sandboxPolicy()},
	)
	steps := []scriptedCompletion{
		{result: protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID: "plan-1", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "update_plan",
				Arguments: json.RawMessage(`{"steps":[{"title":"Implement","status":"in_progress"},{"title":"Verify","status":"pending"}]}`),
			},
		}}}},
		{result: protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID: "write-1", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"result.txt","content":"done\n"}`),
			},
		}}}},
		{result: protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID: "plan-2", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "update_plan",
				Arguments: json.RawMessage(`{"steps":[{"title":"Implement","status":"completed"},{"title":"Verify","status":"completed"}]}`),
			},
		}}}},
		{result: protocol.CompletionResult{Content: "done"}},
	}
	caller := &scriptedToolCaller{steps: steps}
	var progressEvents []ChatStreamEvent
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model:    "test-model",
		Messages: []protocol.Message{{Role: "user", Content: "implement"}},
	}, caller.Complete, func(event ChatStreamEvent) {
		if event.Type == "progress" {
			progressEvents = append(progressEvents, event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var progress *tools.ResultPart
	for index := range outcome.Parts {
		if outcome.Parts[index].Kind == tools.PartProgress {
			progress = &outcome.Parts[index]
		}
	}
	if progress == nil || progress.TaskStatus != "completed" || progress.ChangedFiles != 1 || progress.Additions != 1 || progress.Deletions != 0 {
		t.Fatalf("progress=%+v parts=%+v", progress, outcome.Parts)
	}
	if len(progressEvents) < 3 || progressEvents[len(progressEvents)-1].Progress.TaskStatus != "completed" {
		t.Fatalf("progress events=%+v", progressEvents)
	}
}

func TestToolLoopForwardsLiveStructuredToolOutput(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(streamingProgressTool{})
	completionCalls := 0
	events := make([]ChatStreamEvent, 0, 5)
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "run probe"}},
	}, func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "probe-call", Type: "function", Function: protocol.ToolCallFunction{Name: "stream_probe", Arguments: json.RawMessage(`{}`)},
			}}}, nil
		}
		return protocol.CompletionResult{Content: "done"}, nil
	}, func(event ChatStreamEvent) {
		if event.Type == "tool" {
			events = append(events, event)
		}
	})
	if err != nil || outcome.Content != "done" {
		t.Fatalf("outcome = %#v, err = %v", outcome, err)
	}
	if len(events) < 4 {
		t.Fatalf("tool events = %#v, want start, two output updates, and completion", events)
	}
	for _, event := range events {
		if event.ToolCallID != "probe-call" {
			t.Fatalf("tool event lost call identity: %#v", event)
		}
	}
	foundLiveOutput := false
	foundWaiting := false
	foundCompletedMetadata := false
	for _, event := range events {
		for _, part := range event.Parts {
			foundWaiting = foundWaiting || (event.Status == "waiting" && part.Status == "waiting" && event.Message == "waiting for probe")
			foundLiveOutput = foundLiveOutput || (event.Status == "running" && strings.Contains(part.Stdout, "second"))
			foundCompletedMetadata = foundCompletedMetadata || (event.Status == "completed" && part.ExitCode != nil && part.CompletedAt != "" && part.DurationMs > 0)
		}
	}
	if !foundWaiting || !foundLiveOutput || !foundCompletedMetadata {
		t.Fatalf("waiting=%v live=%v completed=%v events=%#v", foundWaiting, foundLiveOutput, foundCompletedMetadata, events)
	}
}

func TestToolLoopFeedsScreenshotAttachmentToNextModelCall(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(imageAttachmentTool{})
	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "vision-model", Messages: []protocol.Message{{Role: "user", Content: "inspect the screenshot"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "image-call", Type: "function", Function: protocol.ToolCallFunction{Name: "capture_image", Arguments: json.RawMessage(`{}`)},
			}}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "image-call" || len(last.Attachments) != 1 {
			t.Fatalf("screenshot was not attached to tool feedback: %#v", last)
		}
		attachment := last.Attachments[0]
		if attachment.Name != "capture.png" || attachment.MIMEType != "image/png" || attachment.Data != "aGVsbG8=" {
			t.Fatalf("attachment = %#v", attachment)
		}
		return protocol.CompletionResult{Content: "image inspected"}, nil
	}, nil)
	if err != nil || outcome.Content != "image inspected" || completionCalls != 2 {
		t.Fatalf("outcome = %#v, calls = %d, err = %v", outcome, completionCalls, err)
	}
}

func TestToolLoopPersistsSnapshotBeforeFinalCompletion(t *testing.T) {
	workspace := t.TempDir()
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"

	svc.recordUserEvent("baseline")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("baseline", "test-model", nil)
	checkpoints := svc.eventStore.Checkpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want 1", len(checkpoints))
	}
	baselineID := checkpoints[0].ID
	svc.recordUserEvent("create page")

	args, err := json.Marshal(map[string]string{
		"path":    "generated.html",
		"content": "<h1>ok</h1>\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := &scriptedToolCaller{steps: []scriptedCompletion{
		{result: protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: protocol.ToolCallFunction{
				Name:      "write_file",
				Arguments: args,
			},
		}}}},
		{err: errors.New("provider connection dropped")},
	}}

	_, loopErr := svc.runToolLoop(context.Background(), caller, svc.buildToolRegistry(), protocol.ChatRequest{
		Model:    "test-model",
		Messages: []protocol.Message{{Role: "user", Content: "create page"}},
	})
	if loopErr == nil {
		t.Fatal("最终补全失败应返回错误")
	}
	generated := filepath.Join(workspace, "generated.html")
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("工具已成功写入的文件应保留: %v", err)
	}

	foundSnapshot := false
	for _, ev := range svc.eventStore.Events() {
		if ev.Type == eventlog.EventFileSnapshot && ev.Payload.Path == "generated.html" {
			foundSnapshot = true
			break
		}
	}
	if !foundSnapshot {
		t.Fatal("模型最终补全失败前，文件快照事件必须已经持久化")
	}

	if _, err := svc.RewindToCheckpoint(baselineID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("rewind 应删除失败轮次新建的文件，stat err=%v", err)
	}
}

func TestToolLoopFallsBackWithoutToolsForExplicitCompatibilityError(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	var events []ChatStreamEvent
	registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls})
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "answer even without tools"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			if len(request.Tools) == 0 || request.ToolChoice == "none" {
				t.Fatal("initial request did not expose the registered tools")
			}
			return protocol.CompletionResult{}, protocol.NewProviderError(protocol.ProviderErrorInfo{
				HTTPStatus: http.StatusBadRequest,
				Type:       "invalid_request_error",
				Code:       "unknown_parameter",
				Message:    `Unknown parameter: "tools"`,
			})
		case 2:
			if len(request.Tools) != 0 || request.ToolChoice != "none" {
				t.Fatalf("compatibility retry still exposed tools: %#v", request)
			}
			return protocol.CompletionResult{Content: "plain response"}, nil
		default:
			t.Fatalf("unexpected completion call %d", completionCalls)
			return protocol.CompletionResult{}, nil
		}
	}, func(event ChatStreamEvent) { events = append(events, event) })
	if err != nil || outcome.Content != "plain response" || completionCalls != 2 || toolCalls != 0 {
		t.Fatalf("outcome=%#v calls=%d toolCalls=%d err=%v", outcome, completionCalls, toolCalls, err)
	}
	foundNotice := false
	for _, event := range events {
		if event.Status == "retrying" && strings.Contains(event.Message, "不支持工具调用") {
			foundNotice = true
			break
		}
	}
	if !foundNotice {
		t.Fatalf("tool compatibility downgrade was not observable: %#v", events)
	}
}

func TestToolLoopDoesNotDisableToolsForUnrelatedInitialErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "policy", err: protocol.NewProviderError(protocol.ProviderErrorInfo{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "invalid_request",
			Code:       "cyber_policy",
			Message:    "request blocked by policy",
		})},
		{name: "unrelated bad request", err: protocol.NewProviderError(protocol.ProviderErrorInfo{
			HTTPStatus: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "invalid_prompt",
			Message:    "prompt is invalid",
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
			toolCalls := 0
			completionCalls := 0
			registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls})
			_, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
				Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "run a tool"}},
			}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
				completionCalls++
				if len(request.Tools) == 0 || request.ToolChoice == "none" {
					t.Fatal("initial failure was incorrectly retried with tools disabled")
				}
				return protocol.CompletionResult{}, test.err
			}, nil)
			if err == nil {
				t.Fatal("initial provider failure was swallowed")
			}
			if completionCalls != 1 || toolCalls != 0 {
				t.Fatalf("completionCalls=%d toolCalls=%d err=%v", completionCalls, toolCalls, err)
			}
		})
	}
}

func TestToolLoopContinuesBeyondFormerFixedCallLimit(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	registry := tools.NewRegistry(longTaskStepTool{calls: &toolCalls})
	complete := func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls <= 40 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: fmt.Sprintf("long-step-%d", completionCalls), Type: "function",
				Function: protocol.ToolCallFunction{
					Name: "long_task_step", Arguments: json.RawMessage(fmt.Sprintf(`{"step":%d}`, completionCalls)),
				},
			}}}, nil
		}
		return protocol.CompletionResult{Content: "long task completed"}, nil
	}
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "完成长任务"}},
	}, complete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if toolCalls != 40 || completionCalls != 41 || outcome.Content != "long task completed" {
		t.Fatalf("toolCalls=%d completionCalls=%d content=%q", toolCalls, completionCalls, outcome.Content)
	}
}

func TestToolLoopStopsRepeatedCallWithUnchangedResult(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls})
	var finalRequest protocol.ChatRequest
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "probe until done"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if request.ToolChoice == "none" {
			finalRequest = request
			return protocol.CompletionResult{Content: "stopped safely"}, nil
		}
		return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID:       fmt.Sprintf("probe-%d", completionCalls),
			Function: protocol.ToolCallFunction{Name: "cycle_probe", Arguments: json.RawMessage(`{"target":"same"}`)},
		}}}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if toolCalls != 3 || completionCalls != 4 || outcome.Content != "stopped safely" {
		t.Fatalf("toolCalls=%d completionCalls=%d outcome=%#v", toolCalls, completionCalls, outcome)
	}
	if finalRequest.ToolChoice != "none" || len(finalRequest.Tools) != 0 {
		t.Fatalf("final request still exposed tools: %#v", finalRequest)
	}
	if len(finalRequest.Messages) == 0 || !strings.Contains(finalRequest.Messages[len(finalRequest.Messages)-1].Content, "安全熔断") {
		t.Fatalf("final feedback does not explain the circuit breaker: %#v", finalRequest.Messages)
	}
}

func TestToolLoopStopsAlternatingNoProgressCycle(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls})
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "alternate probes"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if request.ToolChoice == "none" {
			return protocol.CompletionResult{Content: "cycle summarized"}, nil
		}
		variant := "a"
		if completionCalls%2 == 0 {
			variant = "b"
		}
		return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID:       fmt.Sprintf("probe-%d", completionCalls),
			Function: protocol.ToolCallFunction{Name: "cycle_probe", Arguments: json.RawMessage(fmt.Sprintf(`{"variant":%q}`, variant))},
		}}}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if toolCalls != 6 || completionCalls != 7 || outcome.Content != "cycle summarized" {
		t.Fatalf("toolCalls=%d completionCalls=%d outcome=%#v", toolCalls, completionCalls, outcome)
	}
}

func TestToolLoopAllowsRepeatedCallWhenResultChanges(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls, changing: true})
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "poll changing progress"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if request.ToolChoice == "none" {
			t.Fatal("changing results must not trigger the no-progress circuit breaker")
		}
		if completionCalls <= 12 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID:       fmt.Sprintf("probe-%d", completionCalls),
				Function: protocol.ToolCallFunction{Name: "cycle_probe", Arguments: json.RawMessage(`{"target":"same"}`)},
			}}}, nil
		}
		return protocol.CompletionResult{Content: "polling completed"}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if toolCalls != 12 || completionCalls != 13 || outcome.Content != "polling completed" {
		t.Fatalf("toolCalls=%d completionCalls=%d outcome=%#v", toolCalls, completionCalls, outcome)
	}
}

func TestToolLoopStopsProviderThatIgnoresDisabledTools(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	toolCalls := 0
	completionCalls := 0
	registry := tools.NewRegistry(cycleProbeTool{calls: &toolCalls})
	outcome, err := svc.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "ignore disabled tools"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		content := ""
		if request.ToolChoice == "none" {
			content = "provider ignored the request"
		}
		return protocol.CompletionResult{Content: content, ToolCalls: []protocol.ToolCall{{
			ID:       fmt.Sprintf("probe-%d", completionCalls),
			Function: protocol.ToolCallFunction{Name: "cycle_probe", Arguments: json.RawMessage(`{"target":"same"}`)},
		}}}, nil
	}, nil)
	if !errors.Is(err, errProviderIgnoredDisabledTools) {
		t.Fatalf("tool-disable violation error = %v", err)
	}
	if toolCalls != 3 || completionCalls != 4 {
		t.Fatalf("provider loop was not stopped: toolCalls=%d completionCalls=%d", toolCalls, completionCalls)
	}
	if outcome.Content != "provider ignored the request" {
		t.Fatalf("host rewrote provider content: %q", outcome.Content)
	}
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartProgress && part.TaskStatus == "completed" {
			t.Fatalf("tool-disable violation was marked completed: %#v", outcome.Parts)
		}
	}
}

func TestToolLoopGuardDeduplicatesRepeatedSSHConnectivityTest(t *testing.T) {
	call := protocol.ToolCall{
		ID: "ssh-test",
		Function: protocol.ToolCallFunction{
			Name:      "ssh",
			Arguments: json.RawMessage("{\"action\":\"test\",\"credential_id\":\"echocode-credential://ssh-test\"}"),
		},
	}
	guard := toolLoopGuard{completedSSHCalls: map[string]bool{}}
	if _, _, guarded, _ := guard.before(call); guarded {
		t.Fatal("first SSH connectivity test was unexpectedly guarded")
	}
	guard.after(call, tools.Result{Summary: "ok"}, &protocol.Message{})
	_, _, guarded, hidden := guard.before(call)
	if !guarded || !hidden || guard.forceFinalResponse {
		t.Fatalf("repeat guard = guarded:%v hidden:%v forceFinal:%v", guarded, hidden, guard.forceFinalResponse)
	}
}

func TestToolLoopGuardKeepsModelInControlAfterSecretCapture(t *testing.T) {
	guard := toolLoopGuard{}
	call := protocol.ToolCall{
		ID: "ssh-secret",
		Function: protocol.ToolCallFunction{
			Name:      "ssh",
			Arguments: json.RawMessage("{\"action\":\"capture_secret\",\"credential_id\":\"ssh-test\",\"command\":\"cat /tmp/password\"}"),
		},
	}
	guard.after(call, tools.Result{Parts: []tools.ResultPart{{
		Kind:     tools.PartSecretResult,
		SecretID: "secret-result",
	}}}, &protocol.Message{})
	if guard.forceFinalResponse {
		t.Fatalf("capturing a secret must not make the host declare the whole task complete: %#v", guard)
	}
}

func TestToolLoopGuardBlocksSecondEquivalentFailureBeforeExecution(t *testing.T) {
	guard := toolLoopGuard{turnIndex: 1, resolvedFailures: map[string]bool{}, blockedFailures: map[string]int{}}
	arguments := []json.RawMessage{
		json.RawMessage(`{"command":"deploy --target \"D:\\Site\""}`),
		json.RawMessage(`{"command":"deploy --target 'D:\\Site'"}`),
		json.RawMessage(`{"command":"deploy --target D:\\Site"}`),
	}
	first := protocol.ToolCall{ID: "failed-1", Function: protocol.ToolCallFunction{Name: "run_command", Arguments: arguments[0]}}
	message := protocol.Message{Role: "tool", Content: "Access is denied."}
	guard.after(first, tools.Result{
		Summary: "$ deploy\nexit code 1", IsError: true,
		Parts: []tools.ResultPart{{Kind: tools.PartToolCall, Name: "run_command", Status: "error", Output: "Access is denied."}},
	}, &message)
	if guard.forceFinalResponse || !strings.Contains(message.Content, "echocode_tool_retry_diagnostic") {
		t.Fatalf("first failure diagnosis = force:%v message:%q", guard.forceFinalResponse, message.Content)
	}

	second := protocol.ToolCall{ID: "failed-2", Function: protocol.ToolCallFunction{Name: "run_command", Arguments: arguments[1]}}
	result, toolMessage, blocked, hidden := guard.before(second)
	if !blocked || hidden || !result.IsError || guard.forceFinalResponse {
		t.Fatalf("second equivalent retry = blocked:%v hidden:%v force:%v result:%#v", blocked, hidden, guard.forceFinalResponse, result)
	}
	if !strings.Contains(toolMessage.Content, "blocked_equivalent_retry") || !strings.Contains(toolMessage.Content, "必须先发生实质变化") {
		t.Fatalf("blocked retry diagnosis = %q", toolMessage.Content)
	}

	third := protocol.ToolCall{ID: "failed-3", Function: protocol.ToolCallFunction{Name: "run_command", Arguments: arguments[2]}}
	_, _, blocked, _ = guard.before(third)
	if !blocked || !guard.forceFinalResponse {
		t.Fatalf("repeated blocked retry did not stop the loop: %#v", guard)
	}
}

func TestToolLoopSwitchesFromFailedShellToStructuredOfficeTool(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.ApprovalPolicy = "never"
	shellCalls := 0
	officeCalls := 0
	registry := tools.NewRegistry(
		deterministicFailureTool{calls: &shellCalls},
		namedSuccessTool{name: "spreadsheet_inspect", calls: &officeCalls},
	)
	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "repair report.xlsx"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "shell-1", Function: protocol.ToolCallFunction{Name: "run_command", Arguments: json.RawMessage(`{"command":"python -c \"repair('report.xlsx')\""}`)},
			}}}, nil
		case 2:
			last := request.Messages[len(request.Messages)-1].Content
			if !strings.Contains(last, "spreadsheet_inspect") || !strings.Contains(last, "change_strategy_before_retry") {
				t.Fatalf("first failure did not recommend structured Office tools: %s", last)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "shell-2", Function: protocol.ToolCallFunction{Name: "run_command", Arguments: json.RawMessage(`{"command":"python -c 'repair(\"report.xlsx\")'"}`)},
			}}}, nil
		case 3:
			last := request.Messages[len(request.Messages)-1].Content
			if !strings.Contains(last, "blocked_equivalent_retry") {
				t.Fatalf("equivalent retry was not blocked before execution: %s", last)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "office-1", Function: protocol.ToolCallFunction{Name: "spreadsheet_inspect", Arguments: json.RawMessage(`{"path":"report.xlsx"}`)},
			}}}, nil
		default:
			return protocol.CompletionResult{Content: "switched to structured inspection"}, nil
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if shellCalls != 1 || officeCalls != 1 || completionCalls != 4 {
		t.Fatalf("calls: shell=%d office=%d completion=%d", shellCalls, officeCalls, completionCalls)
	}
	if outcome.Content != "switched to structured inspection" {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestToolLoopStopsProviderThatKeepsRepeatingBlockedFailure(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.ApprovalPolicy = "never"
	shellCalls := 0
	registry := tools.NewRegistry(deterministicFailureTool{calls: &shellCalls})
	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "run the failing command"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if request.ToolChoice == "none" {
			return protocol.CompletionResult{Content: "The original strategy is blocked; no safe alternative was available."}, nil
		}
		return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
			ID: fmt.Sprintf("repeat-%d", completionCalls),
			Function: protocol.ToolCallFunction{
				Name:      "run_command",
				Arguments: json.RawMessage(`{"command":"python -c \"broken('report.xlsx')\""}`),
			},
		}}}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if shellCalls != 1 || completionCalls != 4 {
		t.Fatalf("repeating provider was not stopped: shell=%d completion=%d", shellCalls, completionCalls)
	}
	if !strings.Contains(outcome.Content, "strategy is blocked") {
		t.Fatalf("safe final response = %q", outcome.Content)
	}
}

func TestToolLoopGuardDoesNotMergeDifferentFailedOperations(t *testing.T) {
	guard := toolLoopGuard{}
	for index, target := range []string{"one", "two", "three", "four"} {
		rawArgs, _ := json.Marshal(map[string]string{"command": "deploy --target " + target})
		message := protocol.Message{}
		guard.after(protocol.ToolCall{
			ID:       fmt.Sprintf("failed-%d", index),
			Function: protocol.ToolCallFunction{Name: "run_command", Arguments: rawArgs},
		}, tools.Result{IsError: true, Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: "run_command", Status: "error", Output: "Access is denied.",
		}}}, &message)
	}
	if guard.forceFinalResponse {
		t.Fatal("different operations were incorrectly treated as one repeated failure")
	}
}

func TestToolLoopDowngradesUnsupportedParallelCallsWithoutDisablingTools(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	registry := svc.buildToolRegistry()
	requests := make([]protocol.ChatRequest, 0, 3)
	outcome, err := svc.runToolLoopWithCompletion(
		context.Background(),
		registry,
		protocol.ChatRequest{
			Messages:          []protocol.Message{{Role: "user", Content: "read fixture.txt"}},
			ParallelToolCalls: true,
		},
		func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
			requests = append(requests, request)
			switch len(requests) {
			case 1:
				return protocol.CompletionResult{}, protocol.NewProviderError(protocol.ProviderErrorInfo{
					HTTPStatus: 400,
					Code:       "unknown_parameter",
					Message:    "Unknown parameter: parallel_tool_calls",
				})
			case 2:
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID: "read-after-downgrade",
					Function: protocol.ToolCallFunction{
						Name: "read_file", Arguments: json.RawMessage(`{"path":"fixture.txt"}`),
					},
				}}}, nil
			default:
				return protocol.CompletionResult{Content: "兼容重试后完成"}, nil
			}
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "兼容重试后完成" || len(requests) != 3 {
		t.Fatalf("outcome=%#v requests=%d", outcome, len(requests))
	}
	if !requests[0].ParallelToolCalls || requests[1].ParallelToolCalls || requests[2].ParallelToolCalls {
		t.Fatalf("parallel compatibility sequence = %#v", []bool{
			requests[0].ParallelToolCalls, requests[1].ParallelToolCalls, requests[2].ParallelToolCalls,
		})
	}
	if len(requests[1].Tools) == 0 || len(requests[2].Tools) == 0 || requests[1].ToolChoice == "none" {
		t.Fatal("parallel field downgrade incorrectly disabled function calling")
	}
}

func TestToolLoopDowngradesUnsupportedRequiredToolChoiceWithoutDisablingTools(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	registry := svc.buildToolRegistry()
	requests := make([]protocol.ChatRequest, 0, 6)
	outcome, err := svc.runToolLoopWithCompletion(
		context.Background(),
		registry,
		protocol.ChatRequest{Messages: []protocol.Message{{Role: "user", Content: "读取并检查 fixture.txt"}}},
		func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
			requests = append(requests, request)
			switch len(requests) {
			case 1:
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID:       "read-first",
					Function: protocol.ToolCallFunction{Name: "read_file", Arguments: json.RawMessage(`{"path":"fixture.txt"}`)},
				}}}, nil
			case 2, 3:
				return protocol.CompletionResult{}, nil
			case 4:
				if request.ToolChoice != "required" {
					t.Fatalf("recovery request tool choice = %q, want required", request.ToolChoice)
				}
				return protocol.CompletionResult{}, protocol.NewProviderError(protocol.ProviderErrorInfo{
					HTTPStatus: 400,
					Code:       "invalid_request_error",
					Message:    "Unsupported value for tool_choice: required",
				})
			case 5:
				if request.ToolChoice == "required" || request.ToolChoice == "none" || len(request.Tools) == 0 {
					t.Fatalf("compatibility retry disabled tools: %+v", request)
				}
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID:       "inspect-after-downgrade",
					Function: protocol.ToolCallFunction{Name: "file_info", Arguments: json.RawMessage(`{"path":"fixture.txt"}`)},
				}}}, nil
			default:
				return protocol.CompletionResult{Content: "已自动继续并完成检查。"}, nil
			}
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 6 || outcome.Content != "已自动继续并完成检查。" || !hasSuccessfulToolEvidence(outcome.Parts, "file_info") {
		t.Fatalf("requests=%d outcome=%#v", len(requests), outcome)
	}
}

func TestReadOnlyRuntimeAdvertisesOnlyExecutableCapabilities(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer svc.Close()
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()
	svc.runtimeSettings.SandboxMode = "read-only"
	svc.runtimeSettings.FilesystemAccess = "read-only"
	svc.runtimeSettings.NetworkAccess = true
	svc.runtimeSettings.ShellAccess = true

	registry := svc.buildToolRegistry()
	for _, name := range []string{"read_file", "file_info", "list_dir", "search", "read_repository", "read_webpage", "web_search"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("read-only runtime omitted available tool %q", name)
		}
	}
	for _, name := range []string{
		"write_file", "apply_patch", "copy_file", "delete_file", "download_file",
		"git_repository", "run_command", "ssh", "terminal",
	} {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("read-only runtime advertised unavailable tool %q", name)
		}
	}
	delegation, ok := registry.Get("delegate_task")
	if !ok {
		t.Fatal("read-only runtime omitted read-only delegation")
	}
	delegateTool, ok := delegation.(DelegateTaskTool)
	if !ok || !delegateTool.ReadOnly {
		t.Fatalf("read-only runtime registered writable delegation: %#v", delegation)
	}
}

func TestRepeatedPlanUpdateDoesNotEndToolLoop(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	registry := svc.buildToolRegistry()
	planArgs := json.RawMessage("{\"steps\":[{\"title\":\"检查\",\"status\":\"in_progress\"}]}")
	completionCalls := 0
	outcome, err := svc.runToolLoopWithCompletion(
		context.Background(),
		registry,
		protocol.ChatRequest{Messages: []protocol.Message{{Role: "user", Content: "检查项目"}}},
		func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
			completionCalls++
			switch completionCalls {
			case 1:
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID:       "plan-1",
					Function: protocol.ToolCallFunction{Name: "update_plan", Arguments: planArgs},
				}}}, nil
			case 2:
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID:       "plan-2",
					Function: protocol.ToolCallFunction{Name: "update_plan", Arguments: planArgs},
				}}}, nil
			default:
				return protocol.CompletionResult{Content: "已完成检查"}, nil
			}
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "已完成检查" || completionCalls != 3 {
		t.Fatalf("outcome = %#v, completion calls = %d", outcome, completionCalls)
	}
}

func TestDistinctPlanUpdatesDoNotEndLongTask(t *testing.T) {
	svc, _ := newNativeToolLoopService(t)
	registry := svc.buildToolRegistry()
	completionCalls := 0
	outcome, err := svc.runToolLoopWithCompletion(
		context.Background(),
		registry,
		protocol.ChatRequest{Messages: []protocol.Message{{Role: "user", Content: "完成一个包含很多阶段的任务"}}},
		func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
			completionCalls++
			if completionCalls <= 20 {
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID: fmt.Sprintf("plan-%d", completionCalls),
					Function: protocol.ToolCallFunction{
						Name: "update_plan",
						Arguments: json.RawMessage(fmt.Sprintf(
							`{"steps":[{"title":"阶段 %d","status":"in_progress"}]}`,
							completionCalls,
						)),
					},
				}}}, nil
			}
			return protocol.CompletionResult{Content: "长任务已完成"}, nil
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Content != "长任务已完成" || completionCalls != 21 {
		t.Fatalf("outcome = %#v, completion calls = %d", outcome, completionCalls)
	}
}

func TestExecuteToolCallRendersValidationFailureAsToolCard(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()
	svc.runtimeSettings.FilesystemAccess = "workspace-write"

	result, message := svc.executeToolCall(context.Background(), svc.buildToolRegistry(), protocol.ToolCall{
		ID:   "call-bad-path",
		Type: "function",
		Function: protocol.ToolCallFunction{
			Name:      "list_dir",
			Arguments: json.RawMessage(`{"path":"~"}`),
		},
	})
	if !result.IsError || len(result.Parts) == 0 {
		t.Fatalf("invalid path result = %+v", result)
	}
	part := result.Parts[0]
	if part.Kind != "tool_call" || part.Name != "list_dir" || part.Status != "error" || part.Input != "~" {
		t.Fatalf("failure card = %+v", part)
	}
	if !strings.HasPrefix(message.Content, result.Summary+"\n\nEchoCode tool execution metadata:\n") {
		t.Fatalf("tool feedback should keep the summary before structured metadata, got %q", message.Content)
	}
	if !strings.Contains(message.Content, `"tool":"list_dir"`) ||
		!strings.Contains(message.Content, `"callId":"call-bad-path"`) ||
		!strings.Contains(message.Content, `"status":"error"`) ||
		!strings.Contains(message.Content, `"input":"~"`) {
		t.Fatalf("tool feedback is missing execution metadata: %q", message.Content)
	}
	foundEvidence := false
	for _, resultPart := range result.Parts[1:] {
		if resultPart.Kind == tools.PartText && strings.Contains(resultPart.Text, "相对于当前工作区") {
			foundEvidence = true
			break
		}
	}
	if !foundEvidence {
		t.Fatalf("validation failure discarded structured evidence: %#v", result.Parts)
	}
}

func TestFormatToolResultFeedbackIncludesExecutionEvidence(t *testing.T) {
	exitCode := 17
	feedback := formatToolResultFeedback(tools.Result{
		Summary: "command failed",
		IsError: true,
		Parts: []tools.ResultPart{{
			Kind:             tools.PartToolCall,
			Name:             "run_command",
			ToolCallID:       "call-shell",
			Status:           "error",
			Input:            "go test ./...",
			WorkingDirectory: `C:\work\project`,
			ExitCode:         &exitCode,
			DurationMs:       1250,
			Stdout:           "partial output",
			Stderr:           "compile failed",
		}},
	}, "run_command")

	marker := "EchoCode tool execution metadata:\n"
	index := strings.Index(feedback, marker)
	if index < 0 {
		t.Fatalf("structured execution metadata missing from %q", feedback)
	}
	var metadata toolResultFeedbackMetadata
	if err := json.Unmarshal([]byte(feedback[index+len(marker):]), &metadata); err != nil {
		t.Fatalf("decode execution metadata: %v\nfeedback=%q", err, feedback)
	}
	if metadata.Tool != "run_command" || metadata.CallID != "call-shell" || metadata.Status != "error" ||
		metadata.Input != "go test ./..." || metadata.WorkingDirectory != `C:\work\project` ||
		metadata.ExitCode == nil || *metadata.ExitCode != exitCode || metadata.DurationMs != 1250 ||
		metadata.Stdout != "partial output" || metadata.Stderr != "compile failed" {
		t.Fatalf("execution metadata = %#v", metadata)
	}
}

func TestExecuteToolCallPreservesStructuredResultReturnedWithError(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	result, message := svc.executeToolCall(
		context.Background(),
		tools.NewRegistry(structuredResultErrorTool{}),
		protocol.ToolCall{
			ID: "call-partial-error", Type: "function",
			Function: protocol.ToolCallFunction{Name: "structured_result_error", Arguments: json.RawMessage(`{}`)},
		},
	)
	if !result.IsError || !strings.Contains(result.Summary, "partial inspection result") || !strings.Contains(result.Summary, "post-parse validation failed") {
		t.Fatalf("partial tool error result = %#v", result)
	}
	foundText := false
	foundErrorCard := false
	for _, part := range result.Parts {
		if part.Kind == tools.PartText && part.Text == "configuration file was parsed" {
			foundText = true
		}
		if part.Kind == tools.PartToolCall && part.Name == "structured_result_error" {
			if part.Status != "error" || part.Stdout != "parsed 3 entries" || !strings.Contains(part.Stderr, "post-parse validation failed") {
				t.Fatalf("partial tool execution evidence = %#v", part)
			}
			foundErrorCard = true
		}
	}
	if !foundText || !foundErrorCard {
		t.Fatalf("structured evidence was discarded: %#v", result.Parts)
	}
	if !strings.Contains(message.Content, `"status":"error"`) ||
		!strings.Contains(message.Content, `"stdout":"parsed 3 entries"`) ||
		!strings.Contains(message.Content, `"stderr":"post-parse validation failed"`) {
		t.Fatalf("model feedback lost partial execution evidence: %q", message.Content)
	}
}

func TestEnsureToolErrorPartPreservesNonToolEvidence(t *testing.T) {
	result := ensureToolErrorPart(tools.Result{
		Summary: "validation failed",
		IsError: true,
		Parts: []tools.ResultPart{
			{Kind: tools.PartFile, Path: `C:\work\report.xlsx`},
			{Kind: tools.PartDiff, Path: `C:\work\report.xlsx`, Patch: "binary workbook updated"},
		},
	}, "office_edit", json.RawMessage(`{"path":"C:\\work\\report.xlsx"}`))
	if len(result.Parts) != 3 || result.Parts[0].Kind != tools.PartToolCall ||
		result.Parts[1].Kind != tools.PartFile || result.Parts[2].Kind != tools.PartDiff {
		t.Fatalf("error evidence parts = %#v", result.Parts)
	}
}

func TestToolRegistryIncludesHostOpenFile(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), OpenFile: func(string) error { return nil }})
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()
	if _, ok := svc.buildToolRegistry().Get("open_file"); !ok {
		t.Fatal("desktop service must expose open_file to the model")
	}
}

func TestToolRegistryExposesStructuredWorkspaceToolsToModels(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	registry := svc.buildToolRegistry()
	for _, name := range []string{
		"read_file", "file_info", "list_dir", "search",
		"write_file", "apply_patch", "copy_file", "delete_file",
	} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("model tool registry is missing %s", name)
		}
	}
	snapshot := svc.builtinToolSnapshot()
	for _, name := range []string{"read_file", "apply_patch", "copy_file", "delete_file"} {
		found := false
		for _, descriptor := range snapshot.Tools {
			if descriptor.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("builtin schema snapshot is missing %s: %#v", name, snapshot.Tools)
		}
	}
}

func TestToolRegistryIncludesNetworkToolsOnlyWithNetworkAccess(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = t.TempDir()
	svc.runtimeSettings.NetworkAccess = false
	for _, name := range []string{"read_repository", "read_webpage", "web_search"} {
		if _, ok := svc.buildToolRegistry().Get(name); ok {
			t.Fatalf("network-disabled registry must not expose %s", name)
		}
	}
	svc.runtimeSettings.NetworkAccess = true
	for _, name := range []string{"read_repository", "read_webpage", "web_search"} {
		if _, ok := svc.buildToolRegistry().Get(name); !ok {
			t.Fatalf("network-enabled registry must expose %s", name)
		}
	}
}
