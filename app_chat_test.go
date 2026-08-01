package main

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
)

func TestChatTaskStopCancelsImmediatelyAndForceReleasesStalledTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	task := &chatTask{
		id:                "stalled-task",
		projectID:         "project",
		sessionID:         "session",
		cancel:            cancel,
		acceptingGuidance: true,
		done:              make(chan struct{}),
	}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	app.chat.bySession = map[string]string{chatSessionKey(task.projectID, task.sessionID): task.id}
	app.chat.active = task

	if stopped := app.StopChatMessage(task.id); !stopped {
		t.Fatal("stop request was not accepted")
	}
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("stop did not cancel the task context immediately")
	}

	app.enforceChatTaskStop(task, 10*time.Millisecond)
	task.markDone()
	if active := app.GetActiveChatTask(); active != nil {
		t.Fatalf("stalled task remained active after force release: %#v", active)
	}
	if err := app.requireProjectSessionIdleChat(task.projectID, task.sessionID); err != nil {
		t.Fatalf("force-released conversation remained busy: %v", err)
	}
}

func TestChatTaskCancellationClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if chatTaskWasCancelled(ctx, errors.New("upstream failed")) {
		t.Fatal("ordinary provider errors must not be reported as user cancellation")
	}
	cancel()
	if !chatTaskWasCancelled(ctx, context.Canceled) {
		t.Fatal("cancelled task must be reported as cancellation")
	}
}

func TestChatTaskRunnerTracksSessionsIndependently(t *testing.T) {
	_, cancelA := context.WithCancel(context.Background())
	_, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	t.Cleanup(cancelB)

	taskA := &chatTask{id: "task-a", projectID: "project", sessionID: "session-a", cancel: cancelA, acceptingGuidance: true}
	taskB := &chatTask{id: "task-b", projectID: "project", sessionID: "session-b", cancel: cancelB, acceptingGuidance: true}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{taskA.id: taskA, taskB.id: taskB}
	app.chat.bySession = map[string]string{taskA.sessionID: taskA.id, taskB.sessionID: taskB.id}
	app.chat.active = taskA

	states := app.GetActiveChatTasks()
	if len(states) != 2 {
		t.Fatalf("active tasks = %d, want 2", len(states))
	}
	sessionIDs := []string{states[0].SessionID, states[1].SessionID}
	sort.Strings(sessionIDs)
	if sessionIDs[0] != "session-a" || sessionIDs[1] != "session-b" {
		t.Fatalf("active sessions = %#v", sessionIDs)
	}

	app.finishChatTask(taskA)
	states = app.GetActiveChatTasks()
	if len(states) != 1 || states[0].TaskID != taskB.id {
		t.Fatalf("finishing task A affected task B: %#v", states)
	}
	if err := app.requireSessionIdleChat(taskA.sessionID); err != nil {
		t.Fatalf("finished session remained busy: %v", err)
	}
	if err := app.requireSessionIdleChat(taskB.sessionID); err == nil {
		t.Fatal("running session was reported idle")
	}
}

func TestChatTaskStateTracksStructuredWaitingAndRetrying(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	task := &chatTask{id: "task-status", projectID: "project", sessionID: "session", status: "running", cancel: cancel}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	app.chat.active = task

	app.emitChatTaskEvent(ChatTaskEvent{TaskID: task.id, Type: "tool", Status: "waiting"})
	if state := app.GetActiveChatTask(); state == nil || state.Status != "waiting" {
		t.Fatalf("waiting state = %#v", state)
	}
	app.emitChatTaskEvent(ChatTaskEvent{TaskID: task.id, Type: "status", Status: "retrying"})
	if state := app.GetActiveChatTask(); state == nil || state.Status != "retrying" {
		t.Fatalf("retrying state = %#v", state)
	}
	app.emitChatTaskEvent(ChatTaskEvent{TaskID: task.id, Type: "tool", Status: "completed"})
	if state := app.GetActiveChatTask(); state == nil || state.Status != "running" {
		t.Fatalf("post-tool state = %#v", state)
	}
	app.emitChatTaskEvent(ChatTaskEvent{TaskID: task.id, Type: "heartbeat", Status: "waiting"})
	if state := app.GetActiveChatTask(); state == nil || state.Status != "running" {
		t.Fatalf("heartbeat state = %#v", state)
	}
}

func TestActiveChatTaskRestoresStructuredRuntimeSnapshot(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	if err := service.StartTaskRuntime("task-snapshot", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-snapshot", agent.ChatStreamEvent{Type: "status", Message: "正在核验项目", Status: "waiting", Model: "test-model"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordTaskStreamEvent("task-snapshot", agent.ChatStreamEvent{Type: "delta", Delta: "partial"}); err != nil {
		t.Fatal(err)
	}
	task := &chatTask{
		id: "task-snapshot", projectID: "project", sessionID: "session", service: service,
		startedAt: "2026-07-27T01:02:03Z", status: "waiting",
	}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	app.chat.active = task

	state := app.GetActiveChatTask()
	if state == nil || state.Status != "waiting" || state.Message != "正在核验项目" || state.Model != "test-model" || state.Content != "partial" {
		t.Fatalf("active task snapshot = %#v", state)
	}
	if len(state.Parts) != 1 || state.Parts[0].Kind != "timeline_note" {
		t.Fatalf("active task timeline = %#v", state.Parts)
	}
}

func TestChatTaskDropsLateProgressAfterTerminal(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	if err := service.StartTaskRuntime("task-late-progress", "2026-07-27T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	task := &chatTask{id: "task-late-progress", projectID: "project", sessionID: "session", service: service}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	app.chat.bySession = map[string]string{chatSessionKey(task.projectID, task.sessionID): task.id}
	app.chat.active = task

	app.completeChatTask(task, ChatTaskEvent{
		TaskID:  task.id,
		Type:    "completed",
		Message: "任务完成",
		Result:  &agent.ChatResult{Content: "final answer"},
	})
	app.recordChatTaskProgress(task, agent.ChatStreamEvent{Type: "delta", Delta: " late output"})
	app.recordChatTaskProgress(task, agent.ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "late-call", Status: "running",
	})

	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Status != "completed" || snapshot.Content != "final answer" || len(snapshot.Parts) != 0 {
		t.Fatalf("late progress changed terminal task: %#v ok=%v", snapshot, ok)
	}
}

func TestChatTaskDropsProgressFromSupersededGeneration(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	first, err := service.StartTaskRuntimeWithGeneration("task-generation", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	task := &chatTask{id: "task-generation", projectID: "project", sessionID: "session", service: service, generation: first}
	app := &App{}

	current, err := service.StartTaskRuntimeWithGeneration(task.id, "2026-07-30T01:00:01Z")
	if err != nil {
		t.Fatal(err)
	}
	task.setGeneration(current)
	app.recordChatTaskProgressForGeneration(task, first, agent.ChatStreamEvent{Type: "delta", Delta: "stale"})
	app.recordChatTaskProgressForGeneration(task, current, agent.ChatStreamEvent{Type: "delta", Delta: "current"})

	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Generation != current || snapshot.Content != "current" {
		t.Fatalf("runtime snapshot = %#v ok=%v", snapshot, ok)
	}
}

func TestCancelActiveChatTaskMarksRuntimeCancelling(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	generation, err := service.StartTaskRuntimeWithGeneration("task-cancelling", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	task := &chatTask{
		id: "task-cancelling", projectID: "project", sessionID: "session", service: service,
		generation: generation, cancel: cancel,
	}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	app.chat.active = task

	if cancelled := app.cancelActiveChatTask(task.id); cancelled != task {
		t.Fatalf("cancelled task = %#v", cancelled)
	}
	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Status != "cancelling" || snapshot.Terminal() {
		t.Fatalf("runtime snapshot = %#v ok=%v", snapshot, ok)
	}
}

func TestCancelAllChatTasksMarksEveryRuntimeCancelling(t *testing.T) {
	app := &App{}
	app.chat.tasks = make(map[string]*chatTask)
	app.chat.bySession = make(map[string]string)

	for _, taskID := range []string{"task-cancel-all-a", "task-cancel-all-b"} {
		service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
		generation, err := service.StartTaskRuntimeWithGeneration(taskID, "2026-07-30T01:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		task := &chatTask{
			id: taskID, projectID: "project", sessionID: taskID, service: service,
			generation: generation, cancel: cancel, acceptingGuidance: true,
		}
		app.chat.tasks[taskID] = task
		app.chat.bySession[chatSessionKey(task.projectID, task.sessionID)] = taskID
		app.chat.active = task
		t.Cleanup(service.Close)

		defer func(ctx context.Context, service *agent.Service, taskID string) {
			select {
			case <-ctx.Done():
			default:
				t.Errorf("%s context was not cancelled", taskID)
			}
			snapshot, ok := service.TaskRuntimeSnapshot()
			if !ok || snapshot.Status != "cancelling" || snapshot.Terminal() {
				t.Errorf("%s runtime snapshot = %#v ok=%v", taskID, snapshot, ok)
			}
		}(ctx, service, taskID)
	}

	app.cancelAllChatTasks()
}

func TestProjectSessionIdleCheckDistinguishesDuplicateSessionIDs(t *testing.T) {
	_, cancelA := context.WithCancel(context.Background())
	_, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	t.Cleanup(cancelB)

	taskA := &chatTask{id: "task-a", projectID: "project-a", sessionID: "new-chat", cancel: cancelA}
	taskB := &chatTask{id: "task-b", projectID: "project-b", sessionID: "new-chat", cancel: cancelB}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{taskA.id: taskA, taskB.id: taskB}
	app.chat.bySession = map[string]string{
		chatSessionKey(taskA.projectID, taskA.sessionID): taskA.id,
		chatSessionKey(taskB.projectID, taskB.sessionID): taskB.id,
	}

	if err := app.requireProjectSessionIdleChat("project-a", "new-chat"); err == nil {
		t.Fatal("running project-a conversation was reported idle")
	}
	app.finishChatTask(taskA)
	if err := app.requireProjectSessionIdleChat("project-a", "new-chat"); err != nil {
		t.Fatalf("finished project-a conversation remained busy: %v", err)
	}
	if err := app.requireProjectSessionIdleChat("project-b", "new-chat"); err == nil {
		t.Fatal("project-b task was confused with the identically named project-a conversation")
	}
}

func TestDeleteProjectSessionOnlyChecksTargetConversation(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	running := &chatTask{id: "task-b", projectID: "project-b", sessionID: "new-chat", cancel: cancel}
	app := &App{}
	app.chat.tasks = map[string]*chatTask{running.id: running}
	app.chat.bySession = map[string]string{
		chatSessionKey(running.projectID, running.sessionID): running.id,
	}

	if err := app.requireProjectSessionIdleChat("project-a", "new-chat"); err != nil {
		t.Fatalf("another project's task blocked the target conversation: %v", err)
	}
	if err := app.requireProjectSessionIdleChat("project-b", "new-chat"); err == nil {
		t.Fatal("running target conversation was reported idle")
	}
}

func TestApprovalServiceRoutesToOwningTask(t *testing.T) {
	serviceA := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir()})
	serviceB := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir()})
	app := &App{}
	taskA := &chatTask{id: "task-a", service: serviceA}
	taskB := &chatTask{id: "task-b", service: serviceB}
	app.chat.approvalOwners = map[string]*chatTask{"approval-a": taskA, "approval-b": taskB}

	if got := app.approvalService("approval-b"); got != serviceB {
		t.Fatalf("approval routed to %p, want %p", got, serviceB)
	}
	if got := app.approvalService("approval-b"); got != nil {
		t.Fatal("approval ownership must be consumed once")
	}
	if got := app.approvalService("approval-a"); got != serviceA {
		t.Fatalf("approval routed to %p, want %p", got, serviceA)
	}
}

func TestChatGuidanceQueuesInOrderAndClosesAtomically(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	task := &chatTask{id: "task-1", cancel: cancel, acceptingGuidance: true}
	app := &App{}
	app.chat.active = task

	accepted, err := app.GuideChatMessage("task-1", "guide-1", "先检查测试")
	if err != nil || !accepted {
		t.Fatalf("first guidance accepted=%v err=%v", accepted, err)
	}
	accepted, err = app.GuideChatMessageWithAttachments("task-1", "guide-2", "再看截图", []agent.ChatAttachment{{Name: "screen.png", MIMEType: "image/png", Data: "eA=="}})
	if err != nil || !accepted {
		t.Fatalf("second guidance accepted=%v err=%v", accepted, err)
	}

	first, ok := app.takeNextChatGuidance(task)
	if !ok || first.id != "guide-1" || first.prompt != "先检查测试" {
		t.Fatalf("first guidance = %#v ok=%v", first, ok)
	}
	second, ok := app.takeNextChatGuidance(task)
	if !ok || second.id != "guide-2" || len(second.attachments) != 1 {
		t.Fatalf("second guidance = %#v ok=%v", second, ok)
	}
	if _, ok := app.takeNextChatGuidance(task); ok {
		t.Fatal("empty guidance queue should finish the task")
	}
	if app.GetActiveChatTask() != nil {
		t.Fatal("task remained active after its guidance queue closed")
	}
	accepted, err = app.GuideChatMessage("task-1", "late", "太晚了")
	if err != nil || accepted {
		t.Fatalf("late guidance accepted=%v err=%v", accepted, err)
	}
}

func TestChatGuidanceRejectsWrongTaskAndEmptyMessage(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	app := &App{}
	app.chat.active = &chatTask{id: "task-1", cancel: cancel, acceptingGuidance: true}

	if accepted, err := app.GuideChatMessage("other", "guide", "内容"); err != nil || accepted {
		t.Fatalf("wrong task accepted=%v err=%v", accepted, err)
	}
	if accepted, err := app.GuideChatMessage("task-1", "guide", "   "); err == nil || accepted {
		t.Fatalf("empty guidance accepted=%v err=%v", accepted, err)
	}
}
