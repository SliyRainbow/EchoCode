package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type planFlowProvider struct {
	planCalls    int
	streamCalls  int
	executionErr error
	waitForStop  bool
}

func (p *planFlowProvider) Name() string { return "plan-flow" }
func (p *planFlowProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "plan-model"}}, nil
}
func (p *planFlowProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	p.planCalls++
	return protocol.CompletionResult{Content: "1. 检查工作区\n2. 写入计划产物\n3. 验证结果"}, nil
}
func (p *planFlowProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.streamCalls++
	if p.executionErr != nil {
		return nil, p.executionErr
	}
	events := make(chan protocol.StreamEvent, 2)
	go func() {
		defer close(events)
		if p.waitForStop {
			events <- protocol.StreamEvent{Type: "delta", Delta: "已完成部分分析。"}
			<-ctx.Done()
			return
		}
		if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != "tool" {
			events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
				ID: "plan-write", Type: "function", Function: protocol.ToolCallFunction{
					Name: "write_file", Arguments: json.RawMessage(`{"path":"planned.txt","content":"done\n"}`),
				},
			}}}
			return
		}
		events <- protocol.StreamEvent{Type: "delta", Delta: "计划已执行。"}
	}()
	return events, nil
}

func TestRequestPlanApprovalApprove(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.SetApprovalNotify(func(req ApprovalRequest) {
		if req.Kind != "plan" {
			t.Errorf("kind = %q, want plan", req.Kind)
		}
		go func() { _ = svc.RespondApproval(req.ID, req.Tool, true, "once") }()
	})
	approved, err := svc.requestPlanApproval(context.Background(), "1. 做 A\n2. 做 B")
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("应批准计划")
	}
}

func TestPlanStatePersistsAndRejectsCompletedRegression(t *testing.T) {
	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions}
	svc := NewService(config)
	completed := []tools.ProgressStep{{Title: "inspect", Status: "completed"}, {Title: "verify", Status: "completed"}}
	if err := svc.updatePlanState(completed); err != nil {
		t.Fatal(err)
	}
	if svc.planState.Status != "completed" || svc.planState.Revision != 1 {
		t.Fatalf("plan state = %#v", svc.planState)
	}
	if err := svc.updatePlanState([]tools.ProgressStep{{Title: "inspect", Status: "pending"}}); err == nil {
		t.Fatal("completed step was allowed to move backwards")
	}

	reloaded := NewService(config)
	if reloaded.planState.Status != "completed" || len(reloaded.planState.Steps) != 2 {
		t.Fatalf("reloaded plan state = %#v", reloaded.planState)
	}
}

func TestStartPlanStateAllowsNewPlanToReuseCompletedTitle(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	if err := svc.updatePlanState([]tools.ProgressStep{{Title: "inspect", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.startPlanState([]tools.ProgressStep{
		{Title: "inspect", Status: "pending"},
		{Title: "verify", Status: "pending"},
	}); err != nil {
		t.Fatalf("start new plan: %v", err)
	}
	if svc.planState.Revision != 2 || svc.planState.Status != "running" || len(svc.planState.Steps) != 2 {
		t.Fatalf("plan state = %#v", svc.planState)
	}
}

func TestPlanStepsFromText(t *testing.T) {
	steps := planStepsFromText("1. inspect repository\n2. implement fix\n3. run tests")
	if len(steps) != 3 || steps[0].Title != "inspect repository" || steps[0].Status != "pending" {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestRequestPlanApprovalReject(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.SetApprovalNotify(func(req ApprovalRequest) {
		go func() { _ = svc.RespondApproval(req.ID, req.Tool, false, "once") }()
	})
	approved, err := svc.requestPlanApproval(context.Background(), "some plan")
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("应否决计划")
	}
}

func TestRequestPlanApprovalNoBrokerAutoAllows(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.approvals = nil // 模拟无审批中介
	approved, err := svc.requestPlanApproval(context.Background(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("无审批中介时应默认放行")
	}
}

func TestReadOnlyRegistryHasNoWriteTools(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ShellAccess = true
	svc.runtimeSettings.NetworkAccess = true
	reg := svc.buildReadOnlyRegistry()
	for _, name := range []string{"write_file", "apply_patch", "run_command"} {
		if _, ok := reg.Get(name); ok {
			t.Fatalf("只读注册表不应包含 %s", name)
		}
	}
	for _, name := range []string{"read_file", "list_dir", "search", "read_repository", "web_search"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("只读注册表应包含 %s", name)
		}
	}
}

func TestPlanModeEndToEndApprovesThenExecutes(t *testing.T) {
	svc, workspace, provider := newPlanFlowService(t, ReasoningHigh)
	svc.planMode = true
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		if request.Kind != "plan" {
			t.Errorf("approval kind = %q", request.Kind)
		}
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})

	result, err := svc.SendChatMessage(context.Background(), "按计划创建文件")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "计划已执行。" || provider.planCalls != 1 || provider.streamCalls != 2 {
		t.Fatalf("result=%#v planCalls=%d streamCalls=%d", result, provider.planCalls, provider.streamCalls)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "planned.txt"))
	if err != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "done\n" {
		t.Fatalf("planned file=%q err=%v", data, err)
	}
	if result.State.PlanState.Status != "completed" || len(result.State.PlanState.Steps) != 3 {
		t.Fatalf("plan state = %#v", result.State.PlanState)
	}
	for _, step := range result.State.PlanState.Steps {
		if step.Status != "completed" {
			t.Fatalf("unfinished plan step = %#v", step)
		}
	}
}

func TestPlanModeEndToEndRejectsWithoutWriting(t *testing.T) {
	svc, workspace, provider := newPlanFlowService(t, ReasoningUltra)
	svc.planMode = true
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, false, "once") }()
	})

	result, err := svc.SendChatMessage(context.Background(), "只生成计划")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "选择不执行") || provider.planCalls != 1 || provider.streamCalls != 0 {
		t.Fatalf("result=%#v planCalls=%d streamCalls=%d", result, provider.planCalls, provider.streamCalls)
	}
	if result.State.PlanState.Status != "cancelled" {
		t.Fatalf("rejected plan state = %#v", result.State.PlanState)
	}
	if _, err := os.Stat(filepath.Join(workspace, "planned.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected plan wrote file: %v", err)
	}
}

func TestPlanModeLowReasoningSkipsPlanningPhase(t *testing.T) {
	svc, workspace, provider := newPlanFlowService(t, ReasoningLow)
	svc.planMode = true
	result, err := svc.SendChatMessage(context.Background(), "直接执行小任务")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "计划已执行。" || provider.planCalls != 0 || provider.streamCalls != 2 {
		t.Fatalf("result=%#v planCalls=%d streamCalls=%d", result, provider.planCalls, provider.streamCalls)
	}
	if _, err := os.Stat(filepath.Join(workspace, "planned.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestPlanModeMarksApprovedPlanFailedWhenExecutionFails(t *testing.T) {
	svc, workspace, provider := newPlanFlowService(t, ReasoningHigh)
	svc.planMode = true
	provider.executionErr = errors.New("implementation unavailable")
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})

	_, err := svc.SendChatMessage(context.Background(), "按计划执行但模拟失败")
	if err == nil || !strings.Contains(err.Error(), "implementation unavailable") {
		t.Fatalf("execution error = %v", err)
	}
	if state := svc.WorkbenchState().PlanState; state.Status != "failed" {
		t.Fatalf("failed execution left plan non-terminal: %#v", state)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "planned.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("failed execution unexpectedly wrote a file: %v", statErr)
	}
}

func TestPlanModeCancellationWithPartialOutputPersistsOneTerminalPlanEvent(t *testing.T) {
	svc, _, provider := newPlanFlowService(t, ReasoningHigh)
	svc.planMode = true
	provider.waitForStop = true
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})

	ctx, cancel := context.WithCancel(context.Background())
	result, err := svc.SendChatMessageWithEvents(ctx, "开始执行后停止", func(event ChatStreamEvent) {
		if event.Type == "delta" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !result.TurnCommitted || result.Content != "已完成部分分析。" {
		t.Fatalf("cancelled result = %#v", result)
	}

	cancelledEvents := 0
	for _, event := range svc.eventStore.Events() {
		if event.Type == eventlog.EventPlanUpdate && event.Payload.PlanStatus == "cancelled" {
			cancelledEvents++
		}
	}
	if cancelledEvents != 1 {
		t.Fatalf("cancelled plan events = %d, want 1", cancelledEvents)
	}
	if state := svc.WorkbenchState().PlanState; state.Status != "cancelled" {
		t.Fatalf("cancelled plan state = %#v", state)
	}
}

func TestGuidanceTurnDoesNotRestartPlanApproval(t *testing.T) {
	svc, workspace, provider := newPlanFlowService(t, ReasoningHigh)
	svc.planMode = true
	approvalRequests := 0
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		approvalRequests++
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})

	result, err := svc.SendChatGuidanceWithAttachmentsAndEvents(context.Background(), "调整当前实现", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "计划已执行。" || provider.planCalls != 0 || approvalRequests != 0 {
		t.Fatalf("guidance result=%#v planCalls=%d approvals=%d", result, provider.planCalls, approvalRequests)
	}
	if _, err := os.Stat(filepath.Join(workspace, "planned.txt")); err != nil {
		t.Fatal(err)
	}
}

func newPlanFlowService(t *testing.T, reasoning ReasoningLevel) (*Service, string, *planFlowProvider) {
	t.Helper()
	workspace := t.TempDir()
	provider := &planFlowProvider{}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.reasoning = reasoning
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.SandboxMode = "workspace-write"
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	svc.runtimeSettings.Team.Enabled = false
	svc.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "plan-local", SelectedModelID: "plan-model",
		Providers: []ModelProviderSetting{{
			ID: "plan-local", Name: "Plan Local", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "plan-model",
			Models: []ProviderModel{{ID: "plan-model", DisplayName: "Plan model", Provider: "plan-local", ContextWindowTokens: 128000}},
		}},
	}
	svc.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
	return svc, workspace, provider
}
