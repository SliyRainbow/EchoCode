package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestSubagentRequestUsesBoundedHandoffInsteadOfParentTranscript(t *testing.T) {
	base := protocol.ChatRequest{
		Model: "parent-model",
		Messages: []protocol.Message{
			{Role: "system", Content: "stable agent rules"},
			{Role: "user", Content: "old request that must not be copied"},
			{Role: "assistant", Content: "old assistant answer that must not be copied"},
			{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: "old-call"}}},
			{Role: "tool", ToolCallID: "old-call", Content: "old tool transcript"},
			{Role: "system", InternalKind: contextSummaryKind, Content: "durable parent summary"},
			{Role: "system", InternalKind: contextArtifactKind, Content: "artifact: reports/output.xlsx"},
			{Role: "system", InternalKind: contextFailureStrategyKind, Content: "python quoting failed"},
			{Role: "user", InternalKind: contextRequestKind, Content: "workspace: C:/workspace"},
			{Role: "user", Content: "current parent request", Attachments: []protocol.Attachment{{Name: "screen.png", MIMEType: "image/png", Data: "data"}}},
		},
		Metadata:  map[string]string{"task_kind": "chat"},
		SessionID: "parent-session",
		ThreadID:  "parent-thread",
		TurnID:    "parent-turn",
	}
	spec := delegateTaskSpec{Label: "inspect", Task: "inspect the assigned files", AgentType: subagentReview}
	request := subagentRequest(base, spec, "child-1", chatRoute{ModelID: "child-model"})

	if len(request.Messages) != 4 {
		t.Fatalf("bounded handoff message count = %d, want 4: %#v", len(request.Messages), request.Messages)
	}
	if request.Messages[0].Role != "system" || request.Messages[0].Content != "stable agent rules" {
		t.Fatalf("stable system message = %#v", request.Messages[0])
	}
	handoff := request.Messages[1]
	for _, expected := range []string{"durable parent summary", "reports/output.xlsx", "python quoting failed", "current parent request"} {
		if !strings.Contains(handoff.Content, expected) {
			t.Fatalf("handoff omitted %q: %s", expected, handoff.Content)
		}
	}
	for _, forbidden := range []string{"old request that must not be copied", "old assistant answer", "old tool transcript"} {
		if strings.Contains(handoff.Content, forbidden) {
			t.Fatalf("handoff copied historical transcript %q: %s", forbidden, handoff.Content)
		}
	}
	if handoff.InternalKind != subagentContextKind || len(handoff.Attachments) != 1 || handoff.Attachments[0].Name != "screen.png" {
		t.Fatalf("handoff metadata = %#v", handoff)
	}
	if request.Messages[2].InternalKind != contextRequestKind || !strings.Contains(request.Messages[2].Content, "workspace: C:/workspace") {
		t.Fatalf("private turn context = %#v", request.Messages[2])
	}
	if !strings.Contains(request.Messages[3].Content, spec.Task) || request.Messages[3].Role != "user" {
		t.Fatalf("subagent instruction = %#v", request.Messages[3])
	}
	if len(base.Messages) != 10 || base.Messages[3].ToolCalls[0].ID != "old-call" {
		t.Fatalf("parent request was mutated: %#v", base.Messages)
	}
}

func TestSubagentTaskRegistryPersistsLifecycleAndArtifactSummary(t *testing.T) {
	base := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")})
	service.recordUserEvent("parent anchor")
	scope := subagentExecutionScope{BaseRequest: protocol.ChatRequest{
		Messages:  []protocol.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "build the report"}},
		SessionID: "parent-session", ThreadID: "parent-thread", TurnID: "parent-turn",
	}}
	spec := delegateTaskSpec{Label: "report", Task: "create reports/output.txt", AgentType: subagentImplement}
	part := tools.ResultPart{Kind: tools.PartSubagent, TaskID: "child-persist", AgentType: spec.AgentType, Label: spec.Label, Status: "pending"}
	record := service.newSubagentTaskRecord(scope, spec, part, 42)
	if err := service.persistSubagentTaskRecords([]SubagentTaskRecord{record}); err != nil {
		t.Fatal(err)
	}
	_, control := service.registerSubagentWithRecord(context.Background(), part, record)

	running := part
	running.Status = "running"
	running.ProviderID = "provider"
	running.Model = "model"
	running.CurrentAction = "writing report"
	running.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	control.update(running)
	if err := service.persistSubagentControl(control); err != nil {
		t.Fatal(err)
	}
	completed := running
	completed.Status = "completed"
	completed.Summary = "report created"
	completed.CurrentAction = "done"
	completed.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if !control.finish(delegatedTaskResult{
		part: completed,
		artifacts: []tools.ResultPart{{
			Kind: tools.PartFile, Path: "reports/output.txt", FileAction: "created", Status: "ok",
		}},
	}) {
		t.Fatal("first terminal transition was rejected")
	}
	if err := service.persistSubagentControl(control); err != nil {
		t.Fatal(err)
	}
	if _, finished, newlyCollected := control.collect(); !finished || !newlyCollected {
		t.Fatalf("collect finished=%v newly=%v", finished, newlyCollected)
	}
	if err := service.persistSubagentControl(control); err != nil {
		t.Fatal(err)
	}

	records, err := service.ListSubagentTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("registry records = %#v", records)
	}
	got := records[0]
	if got.TaskID != part.TaskID || got.ParentTaskID != "parent-turn" || got.ParentSessionID != "parent-session" {
		t.Fatalf("task ownership = %#v", got)
	}
	if got.Generation != 42 || got.CheckpointID == "" || got.Status != "completed" || got.RecoveryState != "terminal" || !got.Collected {
		t.Fatalf("recoverable task state = %#v", got)
	}
	if !strings.Contains(got.InputSummary, spec.Task) || got.ResultSummary != "report created" {
		t.Fatalf("task summaries = %#v", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Path != "reports/output.txt" || got.Artifacts[0].Action != "created" {
		t.Fatalf("artifact summaries = %#v", got.Artifacts)
	}
	path, err := service.subagentRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("registry snapshot path=%q info=%v err=%v", path, info, err)
	}
	service.finishSubagentTurn(false)
}

func TestRecoverInterruptedSubagentTasksLeavesExplicitResumeState(t *testing.T) {
	base := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions")}
	service := NewService(config)
	projectID, sessionID := service.subagentRegistryIdentity()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := service.persistSubagentTaskRecords([]SubagentTaskRecord{{
		Version: subagentTaskRegistryVersion, TaskID: "child-stale", ParentTaskID: "parent",
		ProjectID: projectID, SessionID: sessionID, Generation: 7, AgentType: subagentExplore,
		Label: "stale", Status: "running", InputSummary: "inspect", CreatedAt: now, UpdatedAt: now,
		RecoveryState: "active",
	}}); err != nil {
		t.Fatal(err)
	}

	restarted := NewService(config)
	recovered, err := restarted.RecoverInterruptedSubagentTasks()
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered tasks = %d, want 1", recovered)
	}
	records, err := restarted.ListSubagentTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != "interrupted" || records[0].RecoveryState != "worker_not_restored" || !records[0].NeedsResume {
		t.Fatalf("recovered registry = %#v", records)
	}
	if recoveredAgain, err := restarted.RecoverInterruptedSubagentTasks(); err != nil || recoveredAgain != 0 {
		t.Fatalf("second recovery = %d, err=%v", recoveredAgain, err)
	}
}

func TestDelegateTaskPersistsCompletedRegistrationWithoutBlocking(t *testing.T) {
	provider := &subagentProbeProvider{delay: time.Millisecond}
	service, ctx := newSubagentToolTest(t, provider)
	started := time.Now()
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{
		"tasks":[{"label":"inspect","task":"inspect current files","agentType":"explore"}]
	}`))
	if err != nil || result.IsError {
		t.Fatalf("delegate result=%#v err=%v", result, err)
	}
	if time.Since(started) >= 100*time.Millisecond {
		t.Fatal("registry persistence made delegate_task blocking")
	}
	if _, err := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"wait":true}`)); err != nil {
		t.Fatal(err)
	}
	records, err := service.ListSubagentTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != "completed" || !records[0].Collected || records[0].ParentTaskID != "turn-test" {
		t.Fatalf("delegated registry = %#v", records)
	}
	service.finishSubagentTurn(false)
}

func TestSubagentControlIgnoresProgressAfterTerminalState(t *testing.T) {
	part := tools.ResultPart{
		Kind: tools.PartSubagent, TaskID: "child-terminal", AgentType: subagentExplore,
		Label: "terminal", Status: "running", CurrentAction: "working",
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	control := &subagentControl{
		taskID: part.TaskID, done: make(chan struct{}), latest: part,
		taskRecord: SubagentTaskRecord{
			Version: subagentTaskRegistryVersion, TaskID: part.TaskID, Generation: 1,
			AgentType: part.AgentType, Label: part.Label, Status: part.Status,
			CreatedAt: now, UpdatedAt: now, RecoveryState: "active",
		},
	}
	completed := part
	completed.Status = "completed"
	completed.Summary = "finished"
	completed.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if !control.finish(delegatedTaskResult{part: completed}) {
		t.Fatal("terminal transition was rejected")
	}
	late := part
	late.CurrentAction = "late progress"
	if control.update(late) {
		t.Fatal("late progress requested persistence after terminal state")
	}
	latest, _, finished, _ := control.snapshot()
	record := control.taskRecordSnapshot()
	if !finished || latest.Status != "completed" || latest.Summary != "finished" {
		t.Fatalf("late progress replaced terminal result: %#v", latest)
	}
	if record.Status != "completed" || record.RecoveryState != "terminal" {
		t.Fatalf("late progress regressed durable state: %#v", record)
	}
}

func TestSubagentTaskRegistryRejectsStaleTerminalRegression(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "sessions")})
	projectID, sessionID := service.subagentRegistryIdentity()
	baseTime := time.Now().UTC()
	completed := SubagentTaskRecord{
		Version: subagentTaskRegistryVersion, TaskID: "child-monotonic",
		ProjectID: projectID, SessionID: sessionID, Generation: 9,
		AgentType: subagentReview, Label: "review", Status: "completed",
		ResultSummary: "verified result", Collected: true,
		CreatedAt:     baseTime.Format(time.RFC3339Nano),
		UpdatedAt:     baseTime.Add(2 * time.Second).Format(time.RFC3339Nano),
		CompletedAt:   baseTime.Add(2 * time.Second).Format(time.RFC3339Nano),
		RecoveryState: "terminal",
	}
	if err := service.persistSubagentTaskRecords([]SubagentTaskRecord{completed}); err != nil {
		t.Fatal(err)
	}

	staleTerminal := completed
	staleTerminal.ResultSummary = "stale result"
	staleTerminal.Collected = false
	staleTerminal.UpdatedAt = baseTime.Add(time.Second).Format(time.RFC3339Nano)
	if err := service.persistSubagentTaskRecords([]SubagentTaskRecord{staleTerminal}); err != nil {
		t.Fatal(err)
	}
	lateRunning := completed
	lateRunning.Status = "running"
	lateRunning.ResultSummary = ""
	lateRunning.Collected = false
	lateRunning.UpdatedAt = baseTime.Add(3 * time.Second).Format(time.RFC3339Nano)
	lateRunning.CompletedAt = ""
	lateRunning.RecoveryState = "active"
	if err := service.persistSubagentTaskRecords([]SubagentTaskRecord{lateRunning}); err != nil {
		t.Fatal(err)
	}

	records, err := service.ListSubagentTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("registry records = %#v", records)
	}
	got := records[0]
	if got.Status != "completed" || got.ResultSummary != "verified result" || !got.Collected || got.RecoveryState != "terminal" {
		t.Fatalf("registry terminal state regressed: %#v", got)
	}
}
