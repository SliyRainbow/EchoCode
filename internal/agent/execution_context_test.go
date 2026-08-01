package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestProtocolAssistantContentAddsPrivateExecutionCheckpoint(t *testing.T) {
	exitCode := 1
	service := &Service{planState: PlanState{
		Revision: 3,
		Status:   "cancelled",
		Steps: []tools.ProgressStep{
			{Title: "Inspect the workbook", Status: "completed"},
			{Title: "Render and verify", Status: "in_progress"},
		},
	}}
	content := service.protocolAssistantContent("visible answer", []tools.ResultPart{{
		Kind:             tools.PartToolCall,
		Name:             "run_command",
		Status:           "error",
		ToolCallID:       "call-1",
		Input:            `python -c "print('verify')"`,
		WorkingDirectory: `C:\workspace`,
		Stderr:           "SyntaxError: unterminated string literal",
		ExitCode:         &exitCode,
		DurationMs:       120,
	}})

	for _, expected := range []string{
		executionContextStart,
		"plan revision=3 status=cancelled",
		"Render and verify",
		"tool name=run_command status=error",
		"exit_code=1",
		"SyntaxError: unterminated string literal",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("execution checkpoint is missing %q: %q", expected, content)
		}
	}
	if visible := stripPrivateAssistantContext(content); visible != "visible answer" {
		t.Fatalf("visible assistant content = %q", visible)
	}
}

func TestExecutionCheckpointRetainsLatestOperationsWithinBudget(t *testing.T) {
	service := &Service{}
	parts := make([]tools.ResultPart, 0, 24)
	for index := 0; index < 24; index++ {
		parts = append(parts, tools.ResultPart{
			Kind:       tools.PartToolCall,
			Name:       "read_file",
			Status:     "ok",
			ToolCallID: "call-" + string(rune('a'+index)),
			Input:      strings.Repeat("older detail ", 220),
			Output:     strings.Repeat("result ", 220),
		})
	}
	parts[len(parts)-1].Input = "LATEST_OPERATION_PATH"
	checkpoint := service.formatExecutionCheckpoint(parts)
	if !strings.Contains(checkpoint, "LATEST_OPERATION_PATH") {
		t.Fatalf("latest operation was dropped from checkpoint: %q", checkpoint)
	}
	if !strings.Contains(checkpoint, "older_operational_entries_omitted=") {
		t.Fatalf("checkpoint did not disclose budgeted omission: %q", checkpoint)
	}
	if len([]rune(checkpoint)) > executionCheckpointRuneLimit {
		t.Fatalf("checkpoint runes = %d, limit = %d", len([]rune(checkpoint)), executionCheckpointRuneLimit)
	}
}

func TestIncompleteTaskInjectsLatestExecutionCheckpointWithoutKeywordRouting(t *testing.T) {
	service := &Service{planState: PlanState{
		Revision: 1,
		Status:   "cancelled",
		Steps:    []tools.ProgressStep{{Title: "Run verification", Status: "in_progress"}},
	}}
	checkpointed := service.protocolAssistantContent("stopped", []tools.ResultPart{{
		Kind: tools.PartToolCall, Name: "read_file", Status: "ok", Input: `{"path":"report.xlsx"}`, Output: "read complete",
	}})
	service.sessionMessages = []protocol.Message{{
		Role:         "assistant",
		Content:      checkpointed,
		InternalKind: terminalTurnInternalKind("cancelled"),
	}}

	preview := service.contextPreviewForInput("检查刚才生成的文件并修复格式")
	executionState := ""
	for _, section := range preview.VolatileTail {
		if section.Name == "execution_state" {
			executionState = section.Content
			break
		}
	}
	if !strings.Contains(executionState, "read_file") || !strings.Contains(executionState, "Run verification") {
		t.Fatalf("incomplete task context did not restore execution checkpoint: %q", executionState)
	}
	privateContext := formatPrivateTurnContext(preview)
	if !strings.Contains(privateContext, "[execution_state]") || !strings.Contains(privateContext, "read_file") {
		t.Fatalf("execution state was not sent in the private turn context: %q", privateContext)
	}
}

func TestUnrelatedTurnDoesNotInjectCompletedExecutionCheckpoint(t *testing.T) {
	service := &Service{planState: PlanState{
		Revision: 1,
		Status:   "completed",
		Steps:    []tools.ProgressStep{{Title: "Done", Status: "completed"}},
	}}
	service.sessionMessages = []protocol.Message{{Role: "assistant", Content: service.protocolAssistantContent("done", nil)}}

	preview := service.contextPreviewForInput("explain another file")
	for _, section := range preview.VolatileTail {
		if section.Name == "execution_state" && strings.TrimSpace(section.Content) != "" {
			t.Fatalf("completed checkpoint leaked into unrelated turn: %q", section.Content)
		}
	}
}

func TestOlderInterruptedCheckpointDoesNotLeakIntoLaterCompletedWork(t *testing.T) {
	service := &Service{planState: PlanState{
		Revision: 1,
		Status:   "cancelled",
		Steps:    []tools.ProgressStep{{Title: "Old task", Status: "in_progress"}},
	}}
	interrupted := service.protocolAssistantContent("old task stopped", []tools.ResultPart{{
		Kind: tools.PartToolCall, Name: "read_file", Status: "ok", Input: "old.txt", Output: "old result",
	}})
	service.sessionMessages = []protocol.Message{
		{Role: "assistant", Content: interrupted, InternalKind: terminalTurnInternalKind("cancelled")},
		{Role: "user", Content: "开始一个新的无关任务"},
		{Role: "assistant", Content: "新任务已经完成"},
	}

	preview := service.contextPreviewForInput("现在解释另一个文件")
	for _, section := range preview.VolatileTail {
		if section.Name == "execution_state" && strings.TrimSpace(section.Content) != "" {
			t.Fatalf("old interrupted checkpoint leaked into new work: %q", section.Content)
		}
	}
}

func TestWorkbenchStateLockedDoesNotReenterStateMutex(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.stateMu.Lock()
	defer service.stateMu.Unlock()

	done := make(chan struct{})
	go func() {
		_ = service.workbenchStateLocked()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("workbench state reentered stateMu while building context preview")
	}
}
