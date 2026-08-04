package agent

import (
	"encoding/json"
	"testing"
)

// TestNormalizeToolArgsStringEncoded 验证 OpenAI/DeepSeek 的「字符串化 JSON」参数被正确解包。
// 这是导致工具调用全线 "cannot unmarshal string into struct" 的根因修复。
func TestNormalizeToolArgsStringEncoded(t *testing.T) {
	// DeepSeek 实际返回形态：arguments 是被编码成字符串的 JSON。
	raw := json.RawMessage(`"{\"path\":\".\"}"`)
	got := normalizeToolArgs(raw)

	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(got, &args); err != nil {
		t.Fatalf("归一化后应可解析进 struct, err: %v", err)
	}
	if args.Path != "." {
		t.Fatalf("path = %q, want .", args.Path)
	}
}

func TestNormalizeToolArgsPlainObject(t *testing.T) {
	// 个别 provider 直接给对象 → 原样可用。
	raw := json.RawMessage(`{"path":"src"}`)
	got := normalizeToolArgs(raw)
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(got, &args); err != nil {
		t.Fatalf("对象参数应可解析, err: %v", err)
	}
	if args.Path != "src" {
		t.Fatalf("path = %q, want src", args.Path)
	}
}

func TestNormalizeToolArgsEmpty(t *testing.T) {
	for _, in := range []string{``, `""`, `"  "`} {
		got := normalizeToolArgs(json.RawMessage(in))
		if string(got) != "{}" {
			t.Fatalf("空参数 %q 应归一为 {}, got %s", in, got)
		}
	}
}

func TestNormalizeToolArgsCommand(t *testing.T) {
	// run_command 场景：审批框曾显示空命令，就是这里没解开。
	raw := json.RawMessage(`"{\"command\":\"go test ./...\"}"`)
	got := normalizeToolArgs(raw)
	cmd := commandFromArgs(got)
	if cmd != "go test ./..." {
		t.Fatalf("command = %q, want 'go test ./...'", cmd)
	}
}

func TestStructuredRunCommandDisplayIsSharedByApprovalAndTimeline(t *testing.T) {
	raw := json.RawMessage(`{"executable":"python","args":["-c","print('中文 空格')"]}`)
	approval := commandFromArgs(raw)
	timeline := toolInputForDisplay("run_command", raw)
	if approval == "" || approval != timeline {
		t.Fatalf("approval=%q timeline=%q", approval, timeline)
	}
	if approval != `python -c "print('中文 空格')"` {
		t.Fatalf("display = %q", approval)
	}
}

func TestToolInputForDisplaySupportsWebTools(t *testing.T) {
	if got := toolInputForDisplay("web_search", json.RawMessage(`{"query":"宁波天气"}`)); got != "宁波天气" {
		t.Fatalf("web_search input = %q", got)
	}
	if got := toolInputForDisplay("browser", json.RawMessage(`{"action":"open","url":"https://example.com"}`)); got != "https://example.com" {
		t.Fatalf("browser open input = %q", got)
	}
	if got := toolInputForDisplay("browser", json.RawMessage(`{"action":"snapshot"}`)); got != "snapshot" {
		t.Fatalf("browser snapshot input = %q", got)
	}
	if got := toolInputForDisplay("web_search", json.RawMessage(`"{\"query\":\"宁波天气\"}"`)); got != "宁波天气" {
		t.Fatalf("string-encoded web_search input = %q", got)
	}
	if got := toolInputForDisplay("browser", json.RawMessage(`"{\"action\":\"open\",\"url\":\"https://example.com\"}"`)); got != "https://example.com" {
		t.Fatalf("string-encoded browser input = %q", got)
	}
}

func TestToolInputForDisplaySupportsCodeGraphTools(t *testing.T) {
	if got := toolInputForDisplay("mcp__codegraph__codegraph_explore", json.RawMessage(`{"query":"session runtime cancellation"}`)); got != "session runtime cancellation" {
		t.Fatalf("codegraph_explore input = %q", got)
	}
	if got := toolInputForDisplay("mcp__codegraph__codegraph_impact", json.RawMessage(`{"symbol":"NewProjectSessionRuntime"}`)); got != "NewProjectSessionRuntime" {
		t.Fatalf("codegraph_impact input = %q", got)
	}
	if got := toolInputForDisplay("mcp__codegraph__codegraph_node", json.RawMessage(`{"file":"internal/agent/session_runtime.go"}`)); got != "internal/agent/session_runtime.go" {
		t.Fatalf("codegraph_node input = %q", got)
	}
	if got := toolInputForDisplay("mcp__codegraph__codegraph_status", json.RawMessage(`{}`)); got != "检查代码索引状态" {
		t.Fatalf("codegraph_status input = %q", got)
	}
}
