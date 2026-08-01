package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type contextCapturingToolCaller struct {
	requests []protocol.ChatRequest
}

func (c *contextCapturingToolCaller) Complete(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
	c.requests = append(c.requests, request)
	return protocol.CompletionResult{Content: "1. Inspect\n2. Verify"}, nil
}

func TestFormatPrivateTurnContextSendsVolatileSectionsWithoutDuplicatingUserInput(t *testing.T) {
	ctx := RequestContext{VolatileTail: []ContextSection{
		{Name: "user_input", Content: "do not duplicate this request"},
		{Name: "triggered_skills", Content: "skill: spreadsheet\nverify the rendered workbook"},
		{Name: "project_context", Content: "remember the active project"},
		{Name: "output_requirements", Content: "report tests honestly"},
	}}

	content := formatPrivateTurnContext(ctx)
	for _, expected := range []string{requestContextStart, "triggered_skills", "verify the rendered workbook", "project_context", "output_requirements", requestContextEnd} {
		if !strings.Contains(content, expected) {
			t.Fatalf("private context is missing %q: %q", expected, content)
		}
	}
	if strings.Contains(content, "do not duplicate this request") || strings.Contains(content, "[user_input]") {
		t.Fatalf("user input was duplicated into private context: %q", content)
	}
}

func TestAppendTurnRequestMessagesKeepsPrivateContextAdjacentToUser(t *testing.T) {
	messages := []protocol.Message{{Role: "system", Content: "stable"}}
	ctx := RequestContext{VolatileTail: []ContextSection{{Name: "project_context", Content: "project memory"}}}
	messages = appendTurnRequestMessages(messages, ctx, "continue", nil)

	if start := currentTurnMessageStart(messages); start != 1 {
		t.Fatalf("current turn start = %d, want 1", start)
	}
	if len(messages) != 3 || messages[1].InternalKind != contextRequestKind || messages[1].Role != "user" {
		t.Fatalf("private context message = %#v", messages)
	}
	if messages[2].Content != "continue" || messages[2].InternalKind != "" {
		t.Fatalf("visible user message = %#v", messages[2])
	}
}

func TestCompressProtocolMessagesDropsOldPrivateTurnContexts(t *testing.T) {
	messages := []protocol.Message{{Role: "system", Content: "stable"}}
	for index := 0; index < 8; index++ {
		messages = append(messages,
			protocol.Message{Role: "user", Content: "private skill revision " + string(rune('a'+index)), InternalKind: contextRequestKind},
			protocol.Message{Role: "user", Content: strings.Repeat("request ", 180)},
			protocol.Message{Role: "assistant", Content: strings.Repeat("answer ", 180)},
		)
	}
	messages = append(messages,
		protocol.Message{Role: "user", Content: "latest private context", InternalKind: contextRequestKind},
		protocol.Message{Role: "user", Content: "latest request"},
	)

	compressed, removed := compressProtocolMessages(messages, contextBudget{InputLimitTokens: 4_000, TargetTokens: 2_000})
	if removed == 0 {
		t.Fatal("expected old messages and private contexts to be removed")
	}
	privateCount := 0
	for _, message := range compressed {
		if message.InternalKind == contextRequestKind {
			privateCount++
			if !strings.Contains(message.Content, "latest private context") {
				t.Fatalf("compression kept stale private context: %q", message.Content)
			}
		}
		if message.InternalKind == contextSummaryKind && strings.Contains(message.Content, "private skill revision") {
			t.Fatalf("private turn context leaked into compressed summary: %q", message.Content)
		}
	}
	if privateCount != 1 {
		t.Fatalf("private context count after compression = %d, want 1: %#v", privateCount, compressed)
	}
}

func TestPlanTeamAndSubagentRequestsInheritPrivateTurnContext(t *testing.T) {
	private := protocol.Message{Role: "user", Content: requestContextStart + "\n[project_context]\nremember this\n" + requestContextEnd, InternalKind: contextRequestKind}
	base := protocol.ChatRequest{
		Model:             "model-main",
		ParallelToolCalls: true,
		Messages: []protocol.Message{
			{Role: "system", Content: "stable"},
			private,
			{Role: "user", Content: "finish the task"},
		},
		Metadata: map[string]string{"task_kind": "chat"},
	}

	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	caller := &contextCapturingToolCaller{}
	if _, _, err := service.runPlanPhase(context.Background(), caller, base, chatRoute{ModelID: "model-main"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) == 0 || !requestHasPrivateTurnContext(caller.requests[0]) {
		t.Fatalf("plan request lost private turn context: %#v", caller.requests)
	}

	teamRequest := teamRoleRequest(base, TeamRoleReviewer, 1, chatRoute{ModelID: "model-review"}, nil)
	if !requestHasPrivateTurnContext(teamRequest) || teamRequest.Messages[len(teamRequest.Messages)-1].Content == "finish the task" || !teamRequest.ParallelToolCalls {
		t.Fatalf("team request did not preserve context and append role instruction: %#v", teamRequest.Messages)
	}
	synthesisRequest := teamRoleRequest(base, TeamRoleSynthesizer, 1, chatRoute{ModelID: "model-synthesis"}, nil)
	if synthesisRequest.ParallelToolCalls {
		t.Fatal("tool-free synthesis request kept parallel tool calls enabled")
	}

	subagentRequest := subagentRequest(base, delegateTaskSpec{AgentType: subagentReview, Label: "review", Task: "check files"}, "sub-1", chatRoute{ModelID: "model-child"})
	if !requestHasPrivateTurnContext(subagentRequest) || subagentRequest.Messages[len(subagentRequest.Messages)-1].Content == "finish the task" || !subagentRequest.ParallelToolCalls {
		t.Fatalf("subagent request did not preserve context and append child instruction: %#v", subagentRequest.Messages)
	}
}

func requestHasPrivateTurnContext(request protocol.ChatRequest) bool {
	for _, message := range request.Messages {
		if message.InternalKind == contextRequestKind && strings.Contains(message.Content, requestContextStart) {
			return true
		}
	}
	return false
}

func TestSanitizeModelContentRemovesPrivateContextBlocks(t *testing.T) {
	content := "visible answer\n\n" + requestContextStart + "\nprivate\n" + requestContextEnd +
		"\n\n" + executionContextStart + "\nprivate execution\n" + executionContextEnd
	if visible := sanitizeModelContent(content); visible != "visible answer" {
		t.Fatalf("sanitized model content = %q", visible)
	}
}

func TestSanitizeModelContentRemovesTaggedPrivateReasoning(t *testing.T) {
	content := strings.Join([]string{
		"<thinking>private English reasoning</thinking>",
		"用户可见进展。",
		"<analysis>another private block</analysis>",
		"最终答复。",
	}, "\n")
	if visible := sanitizeModelContent(content); visible != "用户可见进展。\n最终答复。" {
		t.Fatalf("tagged reasoning leaked into visible content: %q", visible)
	}
	if visible := sanitizeModelContent("<thinking>unfinished private reasoning"); visible != "" {
		t.Fatalf("unterminated tagged reasoning leaked into visible content: %q", visible)
	}
	if visible := sanitizeModelContent("进展前缀 <thinking>inline private reasoning</thinking> 用户可见结论"); visible != "进展前缀 用户可见结论" {
		t.Fatalf("inline tagged reasoning leaked into visible content: %q", visible)
	}
}

func TestVisibleCompletionContentSuppressesProgressToolPayload(t *testing.T) {
	payload := `{"message":"已定位配置，正在读取字段。","status":"running"}`
	if visible := visibleCompletionContent(payload, nil); visible != "" {
		t.Fatalf("progress arguments leaked into visible content: %q", visible)
	}
	answer := `{"message":"任务已完成","status":"completed"}`
	if visible := visibleCompletionContent(answer, nil); visible != answer {
		t.Fatalf("ordinary final JSON was hidden: %q", visible)
	}
}
