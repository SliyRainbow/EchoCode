package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestReportProgressPublishesRedactedMilestone(t *testing.T) {
	var emitted []tools.ResultPart
	ctx := tools.WithProgressSink(context.Background(), func(part tools.ResultPart) {
		emitted = append(emitted, part)
	})
	result, err := (ReportProgressTool{}).Execute(ctx, json.RawMessage(`{"message":"已读取配置，password=do-not-show；正在验证连接。","status":"waiting"}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Kind != tools.PartTimelineNote || result.Parts[0].Status != "waiting" {
		t.Fatalf("progress parts = %#v", result.Parts)
	}
	if strings.Contains(result.Parts[0].Message, "do-not-show") || !strings.Contains(result.Parts[0].Message, "[已隐藏]") {
		t.Fatalf("progress message was not redacted: %q", result.Parts[0].Message)
	}
	if len(emitted) != 1 || emitted[0].Message != result.Parts[0].Message {
		t.Fatalf("emitted progress = %#v", emitted)
	}
}

func TestToolLoopRendersReportedProgressAsTimelineInsteadOfToolCard(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(ReportProgressTool{})
	completionCalls := 0
	var statuses []ChatStreamEvent
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "执行并汇报"}},
	}, func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "milestone-1", Type: "function", Function: protocol.ToolCallFunction{
					Name: "report_progress", Arguments: json.RawMessage(`{"message":"已确认工作区结构，正在运行验证。"}`),
				},
			}}}, nil
		}
		return protocol.CompletionResult{Content: "验证完成。"}, nil
	}, func(event ChatStreamEvent) {
		if event.Type == "status" || event.Type == "tool" {
			statuses = append(statuses, event)
		}
	})
	if err != nil || outcome.Content != "验证完成。" {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}
	foundTimeline := false
	for _, part := range outcome.Parts {
		if part.Kind == tools.PartTimelineNote && part.Message == "已确认工作区结构，正在运行验证。" && part.ToolCallID == "milestone-1" {
			foundTimeline = true
		}
		if part.Kind == tools.PartToolCall && part.Name == "report_progress" {
			t.Fatalf("reported progress became a tool card: %#v", outcome.Parts)
		}
	}
	if !foundTimeline {
		t.Fatalf("timeline note missing from outcome: %#v", outcome.Parts)
	}
	foundStatus := false
	for _, event := range statuses {
		if event.Type == "tool" && event.ToolName == "report_progress" {
			t.Fatalf("reported progress emitted a tool card event: %#v", statuses)
		}
		if event.Type == "status" && event.ToolCallID == "milestone-1" && event.Message == "已确认工作区结构，正在运行验证。" {
			foundStatus = true
		}
	}
	if !foundStatus {
		t.Fatalf("live timeline status missing: %#v", statuses)
	}
}

func TestToolLoopHidesTaggedReasoningAndDuplicatedProgressArguments(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	registry := tools.NewRegistry(ReportProgressTool{})
	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "执行并汇报"}},
	}, func(_ context.Context, _ protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		if completionCalls == 1 {
			arguments := json.RawMessage(`{"message":"已定位配置，正在读取字段。","status":"running"}`)
			return protocol.CompletionResult{
				Content: "<thinking>private tool planning</thinking>\n" + string(arguments),
				ToolCalls: []protocol.ToolCall{{
					ID: "milestone-private", Type: "function",
					Function: protocol.ToolCallFunction{Name: "report_progress", Arguments: arguments},
				}},
			}, nil
		}
		return protocol.CompletionResult{Content: "<thinking>private final planning</thinking>\n已经完成。"}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(outcome.Parts)
	visible := outcome.Content + "\n" + string(encoded)
	for _, forbidden := range []string{"<thinking>", "private tool planning", `{"message":"已定位配置`} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("private or duplicated tool payload leaked via %q: %s", forbidden, visible)
		}
	}
	if outcome.Content != "已经完成。" {
		t.Fatalf("visible outcome = %q", outcome.Content)
	}
}

func TestToolLoopRequiresSubstantiveActionAfterRecoveryProgress(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.ApprovalPolicy = "never"
	sshCalls := 0
	registry := tools.NewRegistry(ReportProgressTool{}, namedSuccessTool{name: "ssh", calls: &sshCalls})
	completionCalls := 0
	var statuses []ChatStreamEvent

	outcome, err := service.runToolLoopWithCompletion(context.Background(), registry, protocol.ChatRequest{
		Model: "test-model", ToolChoice: "auto",
		Messages: []protocol.Message{{Role: "user", Content: "读取远程应用的账号和密码"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completionCalls++
		switch completionCalls {
		case 1:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "progress-start", Function: protocol.ToolCallFunction{
					Name: "report_progress", Arguments: json.RawMessage(`{"message":"正在验证 SSH 连接。"}`),
				},
			}}}, nil
		case 2:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "ssh-test", Function: protocol.ToolCallFunction{
					Name: "ssh", Arguments: json.RawMessage(`{"action":"test","credential_id":"ssh-test"}`),
				},
			}}}, nil
		case 3:
			return protocol.CompletionResult{}, nil
		case 4:
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "progress-after-test", Function: protocol.ToolCallFunction{
					Name: "report_progress", Arguments: json.RawMessage(`{"message":"SSH 已验证，正在读取部署配置。"}`),
				},
			}}}, nil
		case 5:
			if request.ToolChoice != "required" || len(request.Tools) == 0 {
				t.Fatalf("progress-only recovery did not require a substantive tool: %+v", request)
			}
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
				ID: "ssh-inspect", Function: protocol.ToolCallFunction{
					Name: "ssh", Arguments: json.RawMessage(`{"action":"run","credential_id":"ssh-test","command":"inspect-service"}`),
				},
			}}}, nil
		default:
			return protocol.CompletionResult{Content: "账号和密码已分别通过受保护结果交付。"}, nil
		}
	}, func(event ChatStreamEvent) {
		if event.Type == "status" {
			statuses = append(statuses, event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if completionCalls != 6 || sshCalls != 2 || strings.Contains(outcome.Content, "继续") {
		t.Fatalf("calls=%d sshCalls=%d outcome=%#v", completionCalls, sshCalls, outcome)
	}
	foundRecoveryStatus := false
	for _, event := range statuses {
		if event.Message == "正在执行任务" {
			foundRecoveryStatus = true
		}
		if strings.Contains(event.Message, "调查结果已保留") || strings.Contains(event.Message, "请回复") {
			t.Fatalf("internal recovery instruction leaked into timeline: %#v", statuses)
		}
	}
	if !foundRecoveryStatus {
		t.Fatalf("compact recovery status missing: %#v", statuses)
	}
}
