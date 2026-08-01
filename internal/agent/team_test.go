package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type teamProviderScript struct {
	mu                  sync.Mutex
	calls               map[string]int
	readOnlyToolLeak    bool
	reviewerNeedsChange bool
	implementerErr      error
	synthesizerErr      error
	blockRole           string
	blockStarted        chan struct{}
}

func TestTeamRoleLabelAlwaysReturnsReadableFallback(t *testing.T) {
	if got := teamRoleLabel(""); got != "协作任务" {
		t.Fatalf("empty role label = %q", got)
	}
	if got := teamRoleLabel("security-review"); got != "协作任务（security-review）" {
		t.Fatalf("unknown role label = %q", got)
	}
}

func TestTeamVerdictRequiresExplicitFirstLineProtocol(t *testing.T) {
	for _, test := range []struct {
		content string
		want    string
	}{
		{content: "VERDICT: APPROVED\n验证通过。", want: "approved"},
		{content: "**VERDICT: CHANGES_REQUIRED**\n必须修复边界问题。", want: "changes_required"},
		{content: "测试没有通过，必须修复。", want: "unknown"},
		{content: "说明文本\nVERDICT: APPROVED", want: "unknown"},
	} {
		if got := teamVerdict(TeamRoleReviewer, test.content); got != test.want {
			t.Fatalf("teamVerdict(%q) = %q, want %q", test.content, got, test.want)
		}
	}
}

type scriptedTeamProvider struct {
	role   string
	script *teamProviderScript
}

func (p scriptedTeamProvider) Name() string { return "team-test-" + p.role }

func (p scriptedTeamProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "model-" + p.role}}, nil
}

func (p scriptedTeamProvider) Complete(ctx context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
	return collectProviderStream(ctx, p, request, nil)
}

func (p scriptedTeamProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	if p.script == nil {
		return nil, errors.New("missing team provider script")
	}
	p.script.mu.Lock()
	p.script.calls[p.role]++
	call := p.script.calls[p.role]
	if p.role == TeamRolePlanner || p.role == TeamRoleTester || p.role == TeamRoleReviewer {
		for _, definition := range request.Tools {
			switch definition.Function.Name {
			case "write_file", "apply_patch", "copy_file", "delete_file", "run_command", "terminal":
				p.script.readOnlyToolLeak = true
			}
		}
	}
	needsChange := p.script.reviewerNeedsChange
	implementerErr := p.script.implementerErr
	synthesizerErr := p.script.synthesizerErr
	p.script.mu.Unlock()
	if p.role == p.script.blockRole {
		if p.script.blockStarted != nil {
			close(p.script.blockStarted)
		}
		events := make(chan protocol.StreamEvent)
		go func() {
			<-ctx.Done()
			close(events)
		}()
		return events, nil
	}
	if p.role == TeamRoleImplementer && implementerErr != nil {
		return nil, implementerErr
	}
	if p.role == TeamRoleSynthesizer && synthesizerErr != nil {
		return nil, synthesizerErr
	}

	events := make(chan protocol.StreamEvent, 4)
	go func() {
		defer close(events)
		switch p.role {
		case TeamRolePlanner:
			events <- protocol.StreamEvent{Type: "delta", Delta: "1. 检查现有实现\n2. 完成修改\n3. 核验结果"}
		case TeamRoleImplementer:
			revision := requestContains(request, "正在进行审阅后的修订")
			if lastMessageRole(request) != "tool" {
				if revision {
					events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
						ID: "patch-generated", Type: "function", Function: protocol.ToolCallFunction{
							Name: "apply_patch", Arguments: json.RawMessage(`{"path":"generated.txt","old_string":"first","new_string":"fixed"}`),
						},
					}}}
				} else {
					events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
						ID: "write-generated", Type: "function", Function: protocol.ToolCallFunction{
							Name: "write_file", Arguments: json.RawMessage(`{"path":"generated.txt","content":"first\n"}`),
						},
					}}}
				}
				return
			}
			if revision {
				events <- protocol.StreamEvent{Type: "delta", Delta: "已按审阅反馈修订 generated.txt。"}
			} else {
				events <- protocol.StreamEvent{Type: "delta", Delta: "已创建 generated.txt 并完成实现。"}
			}
		case TeamRoleTester:
			events <- protocol.StreamEvent{Type: "delta", Delta: "VERDICT: APPROVED\n结构化写入结果可读取，未发现回归。"}
		case TeamRoleReviewer:
			if needsChange && call == 1 {
				events <- protocol.StreamEvent{Type: "delta", Delta: "VERDICT: CHANGES_REQUIRED\ngenerated.txt 应将 first 改为 fixed。"}
			} else {
				events <- protocol.StreamEvent{Type: "delta", Delta: "VERDICT: APPROVED\n实现与用户目标一致。"}
			}
		case TeamRoleSynthesizer:
			events <- protocol.StreamEvent{Type: "delta", Delta: "团队已完成实现、测试与审阅。"}
		}
		events <- protocol.StreamEvent{Type: "usage", Usage: &protocol.TokenUsage{PromptTokens: 100, CompletionTokens: 20}}
	}()
	return events, nil
}

func TestTeamModeRunsRolesWithReadOnlyReviewers(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	var events []ChatStreamEvent
	result, err := svc.SendChatMessageWithEvents(context.Background(), "创建一个验证文件", func(event ChatStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "generated.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != "first\n" {
		t.Fatalf("generated content = %q", data)
	}
	if result.Content != "团队已完成实现、测试与审阅。" || result.State.Team.Status != "completed" || result.State.Team.Active {
		t.Fatalf("team result = %#v", result)
	}
	if script.readOnlyToolLeak {
		t.Fatal("planner/tester/reviewer received a mutating tool")
	}
	for _, role := range teamRoleOrder {
		if script.calls[role] == 0 {
			t.Fatalf("role %s was not called: %#v", role, script.calls)
		}
	}
	teamParts := 0
	for _, part := range result.Parts {
		if part.Kind == "team_role" {
			teamParts++
		}
	}
	if teamParts != 5 {
		t.Fatalf("team parts = %d, parts=%#v", teamParts, result.Parts)
	}
	persistedTeamPart := false
	for _, message := range svc.GetSessionMessages() {
		for _, part := range message.Parts {
			if part.Kind == "team_role" && part.Role == TeamRoleReviewer && part.Model == "model-reviewer" && part.Verdict == "approved" {
				persistedTeamPart = true
			}
		}
	}
	if !persistedTeamPart {
		t.Fatal("team role metadata was not restored from the event log")
	}
	teamEvents := 0
	for _, event := range events {
		if event.Type == "team" && event.Team != nil {
			teamEvents++
		}
	}
	if teamEvents < 10 {
		t.Fatalf("team events = %d", teamEvents)
	}
}

func TestTeamModeSkipsImplementerWhenWorkspaceIsReadOnly(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	svc.runtimeSettings.SandboxMode = "read-only"
	svc.runtimeSettings.FilesystemAccess = "read-only"

	result, err := svc.SendChatMessage(context.Background(), "只读分析当前实现")
	if err != nil {
		t.Fatal(err)
	}
	if script.calls[TeamRoleImplementer] != 0 {
		t.Fatalf("read-only team ran implementer: %#v", script.calls)
	}
	for _, role := range []string{TeamRolePlanner, TeamRoleTester, TeamRoleReviewer, TeamRoleSynthesizer} {
		if script.calls[role] == 0 {
			t.Fatalf("read-only team skipped %s: %#v", role, script.calls)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "generated.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only team created a file: %v", err)
	}
	implementer := TeamRoleState{}
	for _, role := range result.State.Team.Roles {
		if role.Role == TeamRoleImplementer {
			implementer = role
			break
		}
	}
	if implementer.Enabled || implementer.Status != "skipped" {
		t.Fatalf("read-only implementer state = %#v", implementer)
	}
	if result.State.Team.Status != "completed" || result.State.Team.Active {
		t.Fatalf("read-only team state = %#v", result.State.Team)
	}
}

func TestReadOnlyTeamResumeSkipsPausedImplementerStage(t *testing.T) {
	settings := DefaultRuntimeSettings().Team
	effective := teamSettingsForPolicy(settings, tools.SandboxPolicy{
		SandboxMode: "read-only", FilesystemAccess: "read-only",
	})
	if roleEnabled(effective, TeamRoleImplementer) {
		t.Fatal("read-only team kept implementer enabled")
	}
	if stage := availableTeamStage(effective, TeamRoleImplementer); stage != TeamRoleTester {
		t.Fatalf("read-only resume stage = %q, want %q", stage, TeamRoleTester)
	}
}

func TestTeamModeRevisesAfterReviewerFeedback(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, true)
	result, err := svc.SendChatMessage(context.Background(), "创建并审阅验证文件")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "generated.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != "fixed\n" {
		t.Fatalf("revision content = %q", data)
	}
	if script.calls[TeamRoleReviewer] != 2 || script.calls[TeamRoleTester] != 2 {
		t.Fatalf("verification calls = %#v", script.calls)
	}
	implementerParts := 0
	for _, part := range result.Parts {
		if part.Kind == "team_role" && part.Role == TeamRoleImplementer {
			implementerParts++
		}
	}
	if implementerParts != 2 {
		t.Fatalf("implementer attempts = %d", implementerParts)
	}
}

func TestTeamPlanRejectionDoesNotRunImplementer(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	svc.planMode = true
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, false, "once") }()
	})
	result, err := svc.SendChatMessage(context.Background(), "先规划再创建文件")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "选择暂不执行") || result.State.Team.Status != "cancelled" {
		t.Fatalf("rejected result = %#v", result)
	}
	if result.State.PlanState.Status != "cancelled" {
		t.Fatalf("rejected plan state = %#v", result.State.PlanState)
	}
	if script.calls[TeamRoleImplementer] != 0 {
		t.Fatalf("implementer calls = %d", script.calls[TeamRoleImplementer])
	}
	if _, err := os.Stat(filepath.Join(workspace, "generated.txt")); !os.IsNotExist(err) {
		t.Fatalf("plan rejection wrote a file: %v", err)
	}
}

func TestTeamPlanApprovalCompletesPlanAndImplementation(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	svc.planMode = true
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})
	result, err := svc.SendChatMessage(context.Background(), "按团队计划创建文件")
	if err != nil {
		t.Fatal(err)
	}
	if result.State.PlanState.Status != "completed" || len(result.State.PlanState.Steps) != 3 {
		t.Fatalf("completed plan state = %#v", result.State.PlanState)
	}
	for _, step := range result.State.PlanState.Steps {
		if step.Status != "completed" {
			t.Fatalf("unfinished team plan step = %#v", step)
		}
	}
	if script.calls[TeamRoleImplementer] == 0 {
		t.Fatal("approved team plan did not run implementer")
	}
	if _, err := os.Stat(filepath.Join(workspace, "generated.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestTeamModeLowReasoningRejectsBeforeAnyRoleRuns(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	svc.reasoning = ReasoningLow
	_, err := svc.SendChatMessage(context.Background(), "低推理下不应运行团队")
	if !errors.Is(err, errTeamModeRequiresPlanner) {
		t.Fatalf("team error = %v", err)
	}
	if len(script.calls) != 0 {
		t.Fatalf("team roles ran before rejection: %#v", script.calls)
	}
	if _, err := os.Stat(filepath.Join(workspace, "generated.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected team mode wrote a file: %v", err)
	}
}

func TestTeamModeCancellationStopsBeforeNextRole(t *testing.T) {
	svc, _, script := newTeamTestService(t, false)
	started := make(chan struct{})
	script.blockRole = TeamRolePlanner
	script.blockStarted = started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.SendChatMessage(ctx, "取消团队任务")
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("planner did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("team cancellation error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("team cancellation did not return promptly")
	}
	script.mu.Lock()
	implementerCalls := script.calls[TeamRoleImplementer]
	script.mu.Unlock()
	if implementerCalls != 0 {
		t.Fatalf("implementer started after cancellation: %d calls", implementerCalls)
	}
	if state := svc.WorkbenchState().Team; state.Status != "paused" || state.Active || state.CurrentRole != TeamRolePlanner {
		t.Fatalf("team state after cancellation = %#v", state)
	}
	if svc.teamResume == nil || svc.teamResume.NextRole != TeamRolePlanner {
		t.Fatalf("team checkpoint after cancellation = %#v", svc.teamResume)
	}
}

func TestTeamModeResumeSkipsCompletedRoles(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	// The resume behavior is independent from path canonicalization. Using the
	// unrestricted test policy avoids managed Windows sandboxes that deny
	// EvalSymlinks for go test's short-lived TempDir.
	svc.runtimeSettings.FilesystemAccess = "unrestricted"
	started := make(chan struct{})
	script.blockRole = TeamRoleReviewer
	script.blockStarted = started
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.SendChatMessage(ctx, "创建文件并在审阅时暂停")
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("reviewer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, errTeamRunPaused) {
			t.Fatalf("pause error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("team pause did not return promptly")
	}

	script.mu.Lock()
	beforeResume := make(map[string]int, len(script.calls))
	for role, calls := range script.calls {
		beforeResume[role] = calls
	}
	script.blockRole = ""
	script.blockStarted = nil
	script.mu.Unlock()
	if beforeResume[TeamRolePlanner] == 0 || beforeResume[TeamRoleImplementer] == 0 || beforeResume[TeamRoleTester] == 0 || beforeResume[TeamRoleReviewer] == 0 {
		t.Fatalf("unexpected calls before resume: %#v", beforeResume)
	}
	if state := svc.WorkbenchState().Team; state.Status != "paused" || state.CurrentRole != TeamRoleReviewer {
		t.Fatalf("paused reviewer state = %#v", state)
	}

	// Simulate reopening the conversation: both the checkpoint and the pause
	// message must be reconstructed from the append-only event log.
	svc.teamResume = nil
	svc.teamState = TeamState{Enabled: true, Status: "idle"}
	svc.rebuildSessionFromEvents()
	if svc.teamResume == nil || svc.teamResume.NextRole != TeamRoleReviewer || svc.teamState.Status != "paused" {
		t.Fatalf("restored team checkpoint = %#v, state=%#v", svc.teamResume, svc.teamState)
	}
	pauseMessageRestored := false
	for _, message := range svc.GetSessionMessages() {
		if message.Role == "assistant" && strings.Contains(message.Content, "使用“继续任务”操作") {
			pauseMessageRestored = true
			break
		}
	}
	if !pauseMessageRestored {
		t.Fatal("pause assistant message was not restored")
	}

	_, err := svc.SendChatMessage(context.Background(), "继续")
	if err == nil || !strings.Contains(err.Error(), "继续任务") {
		t.Fatalf("ordinary message did not require an explicit team lifecycle action: %v", err)
	}
	if svc.teamResume == nil || svc.teamState.Status != "paused" {
		t.Fatalf("ordinary message changed paused team state: checkpoint=%#v state=%#v", svc.teamResume, svc.teamState)
	}

	result, err := svc.ResumePausedTeamTaskWithEvents(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Team.Status != "completed" || result.State.Team.Active {
		t.Fatalf("resumed team result = %#v", result.State.Team)
	}
	if result.Content != "团队已完成实现、测试与审阅。" {
		t.Fatalf("resumed content = %q", result.Content)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "generated.txt")); err != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "first\n" {
		t.Fatalf("resumed file = %q, err=%v", data, err)
	}

	script.mu.Lock()
	afterResume := make(map[string]int, len(script.calls))
	for role, calls := range script.calls {
		afterResume[role] = calls
	}
	script.mu.Unlock()
	if afterResume[TeamRolePlanner] != beforeResume[TeamRolePlanner] || afterResume[TeamRoleImplementer] != beforeResume[TeamRoleImplementer] || afterResume[TeamRoleTester] != beforeResume[TeamRoleTester] {
		t.Fatalf("completed roles were replayed: %#v", afterResume)
	}
	if afterResume[TeamRoleReviewer] != beforeResume[TeamRoleReviewer]+1 || afterResume[TeamRoleSynthesizer] != beforeResume[TeamRoleSynthesizer]+1 {
		t.Fatalf("resume did not continue from reviewer: %#v", afterResume)
	}
	if svc.teamResume != nil {
		t.Fatalf("completed checkpoint remained resumable: %#v", svc.teamResume)
	}
}

func TestResumedTeamFailureSurvivesSessionRebuild(t *testing.T) {
	svc, _, script := newTeamTestService(t, false)
	svc.runtimeSettings.FilesystemAccess = "unrestricted"
	started := make(chan struct{})
	script.blockRole = TeamRoleReviewer
	script.blockStarted = started
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.SendChatMessage(ctx, "创建文件并在审阅时暂停")
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("reviewer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, errTeamRunPaused) {
			t.Fatalf("pause error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("team pause did not return promptly")
	}

	script.mu.Lock()
	script.blockRole = ""
	script.blockStarted = nil
	script.synthesizerErr = errors.New("resume synthesizer unavailable")
	script.mu.Unlock()

	result, err := svc.ResumePausedTeamTaskWithEvents(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "resume synthesizer unavailable") {
		t.Fatalf("resumed team error = %v", err)
	}
	if !result.TurnCommitted || result.State.Team.Status != "failed" || !strings.Contains(result.Content, "未能生成最终汇总") {
		t.Fatalf("resumed failure result = %#v", result)
	}

	svc.teamState = TeamState{Enabled: true, Status: "idle", Roles: []TeamRoleState{}}
	svc.sessionMessages = nil
	svc.rebuildSessionFromEvents()
	if state := svc.WorkbenchState().Team; state.Status != "failed" || state.Active {
		t.Fatalf("rebuilt resumed team state = %#v", state)
	}
	userMessages := 0
	failureMessages := 0
	for _, message := range svc.GetSessionMessages() {
		if message.Role == "user" {
			userMessages++
		}
		if message.Role == "assistant" && message.Status == "failed" && strings.Contains(message.Content, "未能生成最终汇总") {
			failureMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("resume action fabricated a user message: count=%d", userMessages)
	}
	if failureMessages != 1 {
		t.Fatalf("rebuilt resumed failure messages = %d, want 1", failureMessages)
	}
}

func TestAbandonPausedTeamTaskClearsResumptionButKeepsHistory(t *testing.T) {
	svc, _, _ := newTeamTestService(t, false)
	svc.runtimeSettings.FilesystemAccess = "unrestricted"
	svc.teamState = TeamState{
		Enabled:     true,
		Active:      false,
		Status:      "paused",
		CurrentRole: TeamRoleReviewer,
		Roles: []TeamRoleState{{
			Role: TeamRoleReviewer, Label: "审阅", Enabled: true, Status: "paused", Attempt: 1,
		}},
	}
	svc.planState = PlanState{
		Status: "running",
		Steps:  []tools.ProgressStep{{Title: "检查实现", Status: "in_progress"}},
	}
	checkpoint := newTeamRunCheckpoint(svc.runtimeSettings.Team)
	checkpoint.Status = "paused"
	checkpoint.NextRole = TeamRoleReviewer
	checkpoint.PlanStarted = true
	if err := svc.persistTeamRunCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	svc.recordUserEvent("先暂停团队任务")

	state, err := svc.AbandonPausedTeamTask()
	if err != nil {
		t.Fatal(err)
	}
	if svc.teamResume != nil || state.Team.Status != "cancelled" || state.Team.Active {
		t.Fatalf("abandoned team state = checkpoint=%#v team=%#v", svc.teamResume, state.Team)
	}
	if state.PlanState.Status != "cancelled" {
		t.Fatalf("team plan was not cancelled: %#v", state.PlanState)
	}
	if len(svc.GetSessionMessages()) == 0 {
		t.Fatal("abandoning a paused team task discarded session history")
	}
}

func TestTeamPlanMarksPlanAndTeamFailedWhenImplementerFails(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	svc.planMode = true
	script.implementerErr = errors.New("implementer unavailable")
	svc.SetApprovalNotify(func(request ApprovalRequest) {
		go func() { _ = svc.RespondApproval(request.ID, request.Tool, true, "once") }()
	})

	result, err := svc.SendChatMessage(context.Background(), "按团队计划执行但模拟失败")
	if err == nil || !strings.Contains(err.Error(), "implementer unavailable") {
		t.Fatalf("team execution error = %v", err)
	}
	if !result.TurnCommitted || !strings.Contains(result.Content, "任务执行失败") {
		t.Fatalf("failed team turn was not retained: %#v", result)
	}
	state := svc.WorkbenchState()
	if state.PlanState.Status != "failed" || state.Team.Status != "failed" {
		t.Fatalf("failed team execution left non-terminal state: plan=%#v team=%#v", state.PlanState, state.Team)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "generated.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("failed implementer unexpectedly wrote a file: %v", statErr)
	}

	// Simulate switching away or reopening the conversation. The failed team,
	// plan, assistant evidence, and terminal status must survive reconstruction.
	svc.teamState = TeamState{Enabled: true, Status: "idle", Roles: []TeamRoleState{}}
	svc.planState = PlanState{}
	svc.sessionMessages = nil
	svc.rebuildSessionFromEvents()
	restored := svc.WorkbenchState()
	if restored.PlanState.Status != "failed" || restored.Team.Status != "failed" || restored.Team.Active {
		t.Fatalf("rebuilt failed team state: plan=%#v team=%#v", restored.PlanState, restored.Team)
	}
	foundFailure := false
	for _, message := range svc.GetSessionMessages() {
		if message.Role == "assistant" && message.Status == "failed" && strings.Contains(message.Content, "任务执行失败") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Fatal("rebuilt history lost the failed team evidence")
	}
}

func TestTeamSynthesisFailurePreservesCompletedWorkWithoutClaimingSuccess(t *testing.T) {
	svc, workspace, script := newTeamTestService(t, false)
	script.synthesizerErr = errors.New("synthesizer unavailable")

	result, err := svc.SendChatMessage(context.Background(), "创建并审阅验证文件")
	if err == nil || !strings.Contains(err.Error(), "synthesizer unavailable") {
		t.Fatalf("team synthesis error = %v", err)
	}
	if !result.TurnCommitted || !strings.Contains(result.Content, "未能生成最终汇总") || strings.Contains(result.Content, "AI 团队已完成本轮任务") {
		t.Fatalf("preserved synthesis result = %#v", result)
	}
	if state := svc.WorkbenchState(); state.Team.Status != "failed" || state.Team.Active {
		t.Fatalf("team state after synthesis failure = %#v", state.Team)
	}
	data, readErr := os.ReadFile(filepath.Join(workspace, "generated.txt"))
	if readErr != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "first\n" {
		t.Fatalf("completed implementation was not preserved: data=%q err=%v", data, readErr)
	}
}

func TestTeamFinalizeWithoutSynthesizerCannotUseHostGeneratedSuccess(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer svc.Close()
	settings := TeamSettings{Enabled: true, Roles: []TeamRoleSetting{
		{Role: TeamRolePlanner, Enabled: false},
		{Role: TeamRoleImplementer, Enabled: false},
		{Role: TeamRoleTester, Enabled: false},
		{Role: TeamRoleReviewer, Enabled: false},
		{Role: TeamRoleSynthesizer, Enabled: false},
	}}
	svc.runtimeSettings.Team = settings
	messages := []protocol.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "team task"}}
	svc.sessionMessages = cloneProtocolMessages(messages)

	result, err := svc.runTeamTurn(
		context.Background(),
		protocol.ChatRequest{Model: "team-model", Messages: cloneProtocolMessages(messages)},
		chatRoute{Provider: ModelProviderSetting{ID: "team-provider", Name: "Team"}, ModelID: "team-model"},
		requestPrefixDiagnostic{},
		cloneProtocolMessages(messages),
		1,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "缺少可用的模型最终汇总") {
		t.Fatalf("team finalize error = %v, result=%#v", err, result)
	}
	if result.State.Team.Status == "completed" || strings.Contains(result.Content, "设置中未启用 AI 团队汇总角色") {
		t.Fatalf("host-generated team answer was treated as success: %#v", result)
	}
}

func TestGuidanceTurnDoesNotRestartTeamOrchestration(t *testing.T) {
	svc, _, script := newTeamTestService(t, false)
	result, err := svc.SendChatGuidanceWithAttachmentsAndEvents(context.Background(), "调整当前实现", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "model-implementer" {
		t.Fatalf("guidance model = %q", result.Model)
	}
	if script.calls[TeamRoleImplementer] == 0 {
		t.Fatal("guidance did not run the current implementation model")
	}
	for _, role := range []string{TeamRolePlanner, TeamRoleTester, TeamRoleReviewer, TeamRoleSynthesizer} {
		if script.calls[role] != 0 {
			t.Fatalf("guidance restarted team role %s: %#v", role, script.calls)
		}
	}
}

func newTeamTestService(t *testing.T, reviewerNeedsChange bool) (*Service, string, *teamProviderScript) {
	t.Helper()
	workspace := t.TempDir()
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.reasoning = ReasoningUltra
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.SandboxMode = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	models := make([]ProviderModel, 0, len(teamRoleOrder))
	roles := make([]TeamRoleSetting, 0, len(teamRoleOrder))
	for _, role := range teamRoleOrder {
		models = append(models, ProviderModel{ID: "model-" + role, DisplayName: role, Provider: "team-local", ContextWindowTokens: 128000})
		roles = append(roles, TeamRoleSetting{Role: role, Enabled: true, ProviderID: "team-local", ModelID: "model-" + role})
	}
	svc.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "team-local",
		SelectedModelID:    "model-implementer",
		Providers: []ModelProviderSetting{{
			ID: "team-local", Name: "Team Local", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "model-implementer", Models: models,
		}},
	}
	svc.runtimeSettings.Team = TeamSettings{Enabled: true, MaxReviewRounds: 1, Roles: roles}
	script := &teamProviderScript{calls: map[string]int{}, reviewerNeedsChange: reviewerNeedsChange}
	svc.providerFactory = func(route chatRoute) (protocol.Provider, error) {
		role := strings.TrimPrefix(route.ModelID, "model-")
		if !isTeamRole(role) {
			return nil, errors.New("unexpected team model: " + route.ModelID)
		}
		return scriptedTeamProvider{role: role, script: script}, nil
	}
	return svc, workspace, script
}

func requestContains(request protocol.ChatRequest, needle string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func lastMessageRole(request protocol.ChatRequest) string {
	if len(request.Messages) == 0 {
		return ""
	}
	return request.Messages[len(request.Messages)-1].Role
}
