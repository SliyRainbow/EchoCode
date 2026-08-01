package automation

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTasksPersistAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automations.json")
	service, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.Save(Task{
		Name:      "每日检查",
		Enabled:   true,
		Prompt:    "检查项目并汇报测试失败。",
		ProjectID: "project-1",
		SessionID: "session-1",
		Schedule:  Schedule{Kind: "daily", DailyTime: "09:30"},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].ID == "" || state.Tasks[0].NextRunAt == "" {
		t.Fatalf("unexpected saved state: %+v", state)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := reopened.State()
	if len(restored.Tasks) != 1 || restored.Tasks[0].Name != "每日检查" {
		t.Fatalf("unexpected restored state: %+v", restored)
	}
}

func TestRunNowTracksAgentTaskAndCompletion(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "automations.json"))
	if err != nil {
		t.Fatal(err)
	}
	runCount := 0
	service.SetRunner(func(task Task) (string, error) {
		runCount++
		return "chat-task-1", nil
	})
	state, err := service.Save(Task{
		Name:      "测试任务",
		Prompt:    "运行测试。",
		ProjectID: "project-1",
		SessionID: "session-1",
		Schedule:  Schedule{Kind: "interval", IntervalMinutes: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := state.Tasks[0].ID

	state, err = service.RunNow(taskID)
	if err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if runCount != 1 || state.Tasks[0].LastRun == nil || state.Tasks[0].LastRun.Status != "running" || state.Tasks[0].LastRun.ChatTaskID != "chat-task-1" {
		t.Fatalf("unexpected running state: %+v", state)
	}
	if _, err := service.RunNow(taskID); err == nil {
		t.Fatal("second RunNow() should reject an overlapping run")
	}
	state, err = service.MarkStopping(taskID)
	if err != nil || state.Tasks[0].LastRun.Message != "正在停止 Agent" {
		t.Fatalf("MarkStopping() state = %+v, err = %v", state, err)
	}
	if !service.CompleteChatTask("chat-task-1", "completed", "完成") {
		t.Fatal("CompleteChatTask() did not match running task")
	}
	state = service.State()
	if state.Tasks[0].LastRun.Status != "completed" || state.Tasks[0].LastRun.FinishedAt == "" {
		t.Fatalf("unexpected completed state: %+v", state)
	}
}

func TestSetEnabledCalculatesAndClearsNextRun(t *testing.T) {
	service, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.Save(Task{
		Name:      "间隔任务",
		Prompt:    "检查状态。",
		ProjectID: "project-1",
		SessionID: "session-1",
		Schedule:  Schedule{Kind: "interval", IntervalMinutes: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := state.Tasks[0].ID
	state, err = service.SetEnabled(taskID, true)
	if err != nil || state.Tasks[0].NextRunAt == "" {
		t.Fatalf("enable state = %+v, err = %v", state, err)
	}
	state, err = service.SetEnabled(taskID, false)
	if err != nil || state.Tasks[0].NextRunAt != "" {
		t.Fatalf("disable state = %+v, err = %v", state, err)
	}
}

func TestImmediateAgentCompletionIsNotOverwrittenByRunnerReturn(t *testing.T) {
	service, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	service.SetRunner(func(task Task) (string, error) {
		service.AttachChatTask(task.ID, "chat-fast")
		service.CompleteChatTask("chat-fast", "failed", "连接失败")
		return "chat-fast", nil
	})
	state, err := service.Save(Task{
		Name:      "快速失败",
		Prompt:    "执行。",
		ProjectID: "project-1",
		SessionID: "session-1",
		Schedule:  Schedule{Kind: "interval", IntervalMinutes: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = service.RunNow(state.Tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Tasks[0].LastRun == nil || state.Tasks[0].LastRun.Status != "failed" || state.Tasks[0].LastRun.Message != "连接失败" {
		t.Fatalf("immediate completion was overwritten: %+v", state)
	}
}

func TestDailyNextRunUsesNextLocalOccurrence(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 7, 22, 10, 30, 0, 0, location)
	before := nextRun(Schedule{Kind: "daily", DailyTime: "11:00"}, now)
	after := nextRun(Schedule{Kind: "daily", DailyTime: "09:00"}, now)
	if before.Day() != 22 || before.Hour() != 11 {
		t.Fatalf("same-day next run = %s", before)
	}
	if after.Day() != 23 || after.Hour() != 9 {
		t.Fatalf("next-day run = %s", after)
	}
}
