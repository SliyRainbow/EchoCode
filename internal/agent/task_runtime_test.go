package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func newTaskRuntimeTestService(t *testing.T, base string) *Service {
	t.Helper()
	return NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
}

func TestTaskRuntimePersistsLiveToolAndTerminalState(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	if err := service.StartTaskRuntime("task-live", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-live", ChatStreamEvent{Type: "delta", Delta: "partial answer"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-live", ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "call-1", ToolInput: "go test ./...", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	record, ok, err := service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("runtime ok=%v err=%v", ok, err)
	}
	if record.Content != "partial answer" || record.Status != "running" || len(record.Parts) != 1 {
		t.Fatalf("live runtime = %#v", record)
	}
	if err := service.FinishTaskRuntime("task-live", "cancelled", "已停止", ChatResult{}); err != nil {
		t.Fatal(err)
	}
	record, ok, err = service.eventStore.ReadTaskRuntime()
	if err != nil || !ok || record.Status != "cancelled" {
		t.Fatalf("terminal runtime = %#v ok=%v err=%v", record, ok, err)
	}
	if record.Parts[0].Status != "error" || record.Parts[0].Output == "" {
		t.Fatalf("running tool was not settled: %#v", record.Parts[0])
	}
}

func TestTaskRuntimeHeartbeatDoesNotChangeVisibleStatus(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	if err := service.StartTaskRuntime("task-heartbeat", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	service.taskRuntimeLastWrite = time.Time{}
	if err := service.RecordTaskStreamEvent("task-heartbeat", ChatStreamEvent{
		Type: "heartbeat", Message: "upstream still processing", Status: "waiting",
	}); err != nil {
		t.Fatal(err)
	}
	record, ok, err := service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("runtime ok=%v err=%v", ok, err)
	}
	if record.Status != "running" || record.Message != "正在执行任务" || len(record.Parts) != 0 {
		t.Fatalf("heartbeat runtime = %#v", record)
	}
}

func TestTaskRuntimeLiveTerminalStatusKeepsFinalResultWritable(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	if err := service.StartTaskRuntime("task-final-result", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-final-result", ChatStreamEvent{
		Type: "status", Status: "completed", Message: "provider reported finish",
	}); err != nil {
		t.Fatal(err)
	}

	record, ok, err := service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("runtime ok=%v err=%v", ok, err)
	}
	if record.Status != "running" {
		t.Fatalf("live phase incorrectly finalized the task: %#v", record)
	}

	finalPart := tools.ResultPart{Kind: tools.PartToolCall, Name: "write_file", Status: "ok", Output: "saved"}
	if err := service.FinishTaskRuntime("task-final-result", "completed", "task complete", ChatResult{
		Content: "final answer", Parts: []tools.ResultPart{finalPart}, DurationMs: 42,
	}); err != nil {
		t.Fatal(err)
	}
	record, ok, err = service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("final runtime ok=%v err=%v", ok, err)
	}
	if record.Status != "completed" || record.Content != "final answer" || len(record.Parts) != 1 || record.Parts[0].Output != "saved" {
		t.Fatalf("final result was not persisted after a live finish: %#v", record)
	}
}

func TestTaskRuntimeIgnoresLateEventsAfterFinalMerge(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	if err := service.StartTaskRuntime("task-late-event", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-late-event", ChatStreamEvent{Type: "delta", Delta: "partial answer"}); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTaskRuntime("task-late-event", "completed", "task complete", ChatResult{
		Content: "final answer",
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: "write_file", Status: "ok", Output: "saved"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordTaskStreamEvent("task-late-event", ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "late-call", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-late-event", ChatStreamEvent{Type: "delta", Delta: " late output"}); err != nil {
		t.Fatal(err)
	}

	record, ok, err := service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("runtime ok=%v err=%v", ok, err)
	}
	if record.Status != "completed" || record.Content != "final answer" || len(record.Parts) != 1 || record.Parts[0].Name != "write_file" {
		t.Fatalf("late event mutated final runtime: %#v", record)
	}
}

func TestTaskRuntimeFirstTerminalMergeWins(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	if err := service.StartTaskRuntime("task-first-terminal", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTaskRuntime("task-first-terminal", "failed", "first failure", ChatResult{Content: "first result"}); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTaskRuntime("task-first-terminal", "completed", "late completion", ChatResult{Content: "late result"}); err != nil {
		t.Fatal(err)
	}

	record, ok, err := service.eventStore.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("runtime ok=%v err=%v", ok, err)
	}
	if record.Status != "failed" || record.Message != "first failure" || record.Content != "first result" {
		t.Fatalf("late terminal merge replaced the first one: %#v", record)
	}
}

func TestServiceStartupRecoversInterruptedTaskOnce(t *testing.T) {
	base := t.TempDir()
	service := newTaskRuntimeTestService(t, base)
	if err := service.StartTaskRuntime("task-crashed", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	service.recordUserEvent("继续完成报表")
	if _, err := service.eventStore.Append(eventlog.EventPayload{
		PlanStatus: "running",
		PlanSteps:  []eventlog.MessageProgressStep{{Title: "生成报表", Status: "in_progress"}},
	}, eventlog.EventPlanUpdate); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-crashed", ChatStreamEvent{Type: "delta", Delta: "已经生成基础文件。"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-crashed", ChatStreamEvent{
		Type: "tool", ToolName: "render_artifact", ToolCallID: "render-1", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	restarted := newTaskRuntimeTestService(t, base)
	history := restarted.GetSessionMessages()
	if len(history) != 2 {
		t.Fatalf("recovered history = %#v", history)
	}
	terminal := history[1]
	if terminal.Status != "interrupted" || terminal.Content != "已经生成基础文件。" {
		t.Fatalf("recovered terminal = %#v", terminal)
	}
	if len(terminal.Parts) != 2 || terminal.Parts[0].Status != "error" || terminal.Parts[1].Kind != tools.PartText {
		t.Fatalf("recovered tool parts = %#v", terminal.Parts)
	}
	if restarted.planState.Status != "interrupted" || len(restarted.planState.Steps) != 1 {
		t.Fatalf("recovered plan = %#v", restarted.planState)
	}
	record, ok, err := restarted.eventStore.ReadTaskRuntime()
	if err != nil || !ok || record.Status != "interrupted" {
		t.Fatalf("recovered runtime = %#v ok=%v err=%v", record, ok, err)
	}

	restartedAgain := newTaskRuntimeTestService(t, base)
	if got := restartedAgain.GetSessionMessages(); len(got) != 2 {
		t.Fatalf("repeated startup duplicated interrupted turn: %#v", got)
	}
}

func TestStartupDoesNotDuplicateAlreadyCommittedTask(t *testing.T) {
	base := t.TempDir()
	service := newTaskRuntimeTestService(t, base)
	if err := service.StartTaskRuntime("task-committed", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	service.recordUserEvent("完成任务")
	if err := service.RecordTaskStreamEvent("task-committed", ChatStreamEvent{Type: "status", Message: "正在收尾"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.eventStore.Append(eventlog.EventPayload{
		Role: "assistant", Content: "任务完成", Parts: []eventlog.MessagePart{{Kind: string(tools.PartText), Text: "任务完成"}},
	}, eventlog.EventAssistantMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := service.eventStore.Append(eventlog.EventPayload{Label: "任务完成", TurnIndex: 1}, eventlog.EventCheckpoint); err != nil {
		t.Fatal(err)
	}

	restarted := newTaskRuntimeTestService(t, base)
	history := restarted.GetSessionMessages()
	if len(history) != 2 || history[1].Content != "任务完成" || history[1].Status != "" {
		t.Fatalf("committed task was recovered as interrupted: %#v", history)
	}
	record, ok, err := restarted.eventStore.ReadTaskRuntime()
	if err != nil || !ok || record.Status != "completed" {
		t.Fatalf("committed runtime = %#v ok=%v err=%v", record, ok, err)
	}
}

func TestMergeTaskRuntimePartsDoesNotRegressTerminalTimelineNote(t *testing.T) {
	completed := tools.ResultPart{
		Kind:        tools.PartTimelineNote,
		Message:     "SSH 已验证，正在读取部署配置。",
		Status:      "completed",
		ToolCallID:  "progress-1",
		StartedAt:   "2026-07-29T09:00:00Z",
		CompletedAt: "2026-07-29T09:00:05Z",
	}
	staleRunning := tools.ResultPart{
		Kind:       tools.PartTimelineNote,
		Message:    completed.Message,
		Status:     "running",
		ToolCallID: completed.ToolCallID,
	}

	parts := mergeTaskRuntimeParts([]tools.ResultPart{completed}, []tools.ResultPart{staleRunning})
	if len(parts) != 1 || parts[0].Status != "completed" || parts[0].CompletedAt != completed.CompletedAt || parts[0].StartedAt != completed.StartedAt {
		t.Fatalf("terminal timeline note regressed: %#v", parts)
	}
}
