package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type chatTaskRunner struct {
	mu sync.Mutex
	// active is retained as a compatibility pointer for older callers. The
	// maps are authoritative once the application starts normally.
	active         *chatTask
	tasks          map[string]*chatTask
	bySession      map[string]string
	meta           map[string]chatTaskMeta
	approvalOwners map[string]*chatTask
}

type chatTaskMeta struct {
	projectID string
	sessionID string
}

type chatTask struct {
	id                string
	projectID         string
	sessionID         string
	kind              chatTaskKind
	service           *agent.Service
	startedAt         string
	status            string
	cancel            context.CancelFunc
	acceptingGuidance bool
	guidance          []chatGuidance
	done              chan struct{}
	doneOnce          sync.Once
	terminalOnce      sync.Once
	progressMu        sync.Mutex
	generation        uint64
	terminal          bool
}

const chatTaskStopGracePeriod = 2 * time.Second

type chatTaskKind string

const (
	chatTaskKindMessage    chatTaskKind = "message"
	chatTaskKindTeamResume chatTaskKind = "team_resume"
)

type chatGuidance struct {
	id          string
	prompt      string
	attachments []agent.ChatAttachment
}

type ChatTaskState struct {
	TaskID     string             `json:"taskId"`
	StartedAt  string             `json:"startedAt"`
	UpdatedAt  string             `json:"updatedAt,omitempty"`
	ProjectID  string             `json:"projectId,omitempty"`
	SessionID  string             `json:"sessionId,omitempty"`
	Status     string             `json:"status"`
	Message    string             `json:"message,omitempty"`
	Model      string             `json:"model,omitempty"`
	Content    string             `json:"content,omitempty"`
	Reasoning  string             `json:"reasoning,omitempty"`
	DurationMs int64              `json:"durationMs,omitempty"`
	Parts      []tools.ResultPart `json:"parts,omitempty"`
}

type ChatTaskEvent struct {
	TaskID      string                         `json:"taskId"`
	ProjectID   string                         `json:"projectId,omitempty"`
	SessionID   string                         `json:"sessionId,omitempty"`
	Type        string                         `json:"type"`
	Delta       string                         `json:"delta,omitempty"`
	Message     string                         `json:"message,omitempty"`
	Model       string                         `json:"model,omitempty"`
	ToolName    string                         `json:"toolName,omitempty"`
	ToolCallID  string                         `json:"toolCallId,omitempty"`
	ToolInput   string                         `json:"toolInput,omitempty"`
	Status      string                         `json:"status,omitempty"`
	Usage       *protocol.TokenUsage           `json:"usage,omitempty"`
	UsageState  *agent.LiveUsageState          `json:"usageState,omitempty"`
	Progress    *tools.ResultPart              `json:"progress,omitempty"`
	Parts       []tools.ResultPart             `json:"parts,omitempty"`
	Compression *agent.ContextCompressionEvent `json:"compression,omitempty"`
	Team        *agent.TeamRoleEvent           `json:"team,omitempty"`
	GuidanceID  string                         `json:"guidanceId,omitempty"`
	Guidance    string                         `json:"guidance,omitempty"`
	Attachments []agent.ChatAttachment         `json:"attachments,omitempty"`
	Result      *agent.ChatResult              `json:"result,omitempty"`
	Forced      bool                           `json:"forced,omitempty"`
}

func (a *App) RevealSecretResult(projectID, sessionID, secretID string) (agent.SecretResultReveal, error) {
	if a.service == nil {
		return agent.SecretResultReveal{}, errors.New("Agent 服务尚未初始化")
	}
	return a.service.RevealSecretResult(projectID, sessionID, secretID)
}

func (a *App) StartChatMessage(prompt string) (string, error) {
	return a.startChatMessageForProjectSession("", "", prompt, nil)
}

func (a *App) StartChatMessageWithAttachments(prompt string, attachments []agent.ChatAttachment) (string, error) {
	return a.startChatMessageForProjectSession("", "", prompt, attachments)
}

func (a *App) StartChatMessageForSession(sessionID, prompt string) (string, error) {
	return a.startChatMessageForProjectSession("", sessionID, prompt, nil)
}

func (a *App) StartChatMessageForSessionWithAttachments(sessionID, prompt string, attachments []agent.ChatAttachment) (string, error) {
	return a.startChatMessageForProjectSession("", sessionID, prompt, attachments)
}

func (a *App) StartChatMessageForProjectSession(projectID, sessionID, prompt string) (string, error) {
	return a.startChatMessageForProjectSession(projectID, sessionID, prompt, nil)
}

func (a *App) StartChatMessageForProjectSessionWithAttachments(projectID, sessionID, prompt string, attachments []agent.ChatAttachment) (string, error) {
	return a.startChatMessageForProjectSession(projectID, sessionID, prompt, attachments)
}

func (a *App) startChatMessageForProjectSession(projectID, sessionID, prompt string, attachments []agent.ChatAttachment) (string, error) {
	return a.startChatMessageForProjectSessionRoute(projectID, sessionID, prompt, attachments, "", "")
}

func (a *App) startChatMessageForProjectSessionRoute(projectID, sessionID, prompt string, attachments []agent.ChatAttachment, providerID, modelID string) (string, error) {
	return a.startChatMessageForProjectSessionRouteRegistered(projectID, sessionID, prompt, attachments, providerID, modelID, nil)
}

func (a *App) startChatMessageForProjectSessionRouteRegistered(projectID, sessionID, prompt string, attachments []agent.ChatAttachment, providerID, modelID string, registered func(string)) (string, error) {
	return a.startChatMessageForProjectSessionRouteRegisteredWithKind(
		projectID, sessionID, prompt, attachments, providerID, modelID, chatTaskKindMessage, registered,
	)
}

// ResumePausedTeamTask starts an explicit continuation of a persisted team
// checkpoint. It does not create a synthetic user message, so ordinary chat
// text can never be mistaken for a task lifecycle command.
func (a *App) ResumePausedTeamTask(projectID, sessionID string) (string, error) {
	return a.startChatMessageForProjectSessionRouteRegisteredWithKind(
		projectID, sessionID, "", nil, "", "", chatTaskKindTeamResume, nil,
	)
}

// AbandonPausedTeamTask ends the checkpoint after the user explicitly chooses
// to do so. The session history and its audit trail remain intact.
func (a *App) AbandonPausedTeamTask(projectID, sessionID string) (agent.WorkbenchState, error) {
	if a.service == nil {
		return agent.WorkbenchState{}, errors.New("Agent 服务尚未初始化")
	}
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if err := a.requireProjectSessionIdleChat(projectID, sessionID); err != nil {
		return a.service.WorkbenchState(), err
	}
	runtime, err := a.service.NewProjectSessionRuntime(projectID, sessionID)
	if err != nil {
		return a.service.WorkbenchState(), err
	}
	state, err := runtime.AbandonPausedTeamTask()
	if err != nil {
		return state, err
	}
	if activeState, reloaded, reloadErr := a.service.ReloadProjectSessionIfActive(projectID, sessionID); reloadErr == nil && reloaded {
		return activeState, nil
	}
	return state, nil
}

func (a *App) startChatMessageForProjectSessionRouteRegisteredWithKind(
	projectID, sessionID, prompt string,
	attachments []agent.ChatAttachment,
	providerID, modelID string,
	kind chatTaskKind,
	registered func(string),
) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if kind != chatTaskKindTeamResume && prompt == "" && len(attachments) == 0 {
		return "", errors.New("消息内容不能为空")
	}

	if a.service == nil {
		return "", errors.New("Agent 服务尚未初始化")
	}
	activeProjectID, activeSessionID := a.service.ActiveSessionIDs()
	if strings.TrimSpace(sessionID) == "" {
		sessionID = activeSessionID
	}
	if strings.TrimSpace(projectID) == "" && sessionID == activeSessionID {
		projectID = activeProjectID
	}
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("当前没有可用会话")
	}
	runtime, runtimeErr := a.service.NewProjectSessionRuntime(projectID, sessionID)
	if runtimeErr != nil {
		return "", runtimeErr
	}
	if err := runtime.SetEphemeralModelRoute(providerID, modelID); err != nil {
		return "", err
	}
	// The requested session is authoritative. This matters when the user
	// switches to a session owned by another project while another task runs.
	if runtimeProjectID, runtimeSessionID := runtime.ActiveSessionIDs(); runtimeProjectID != "" {
		projectID = runtimeProjectID
		if runtimeSessionID != "" {
			sessionID = runtimeSessionID
		}
	}

	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	task := &chatTask{
		id:                fmt.Sprintf("chat-%d", time.Now().UnixNano()),
		projectID:         projectID,
		sessionID:         sessionID,
		kind:              kind,
		service:           runtime,
		startedAt:         time.Now().Format(time.RFC3339Nano),
		status:            "running",
		cancel:            cancel,
		acceptingGuidance: true,
		done:              make(chan struct{}),
	}

	a.chat.mu.Lock()
	if a.chat.tasks == nil {
		a.chat.tasks = make(map[string]*chatTask)
	}
	if a.chat.bySession == nil {
		a.chat.bySession = make(map[string]string)
	}
	if a.chat.meta == nil {
		a.chat.meta = make(map[string]chatTaskMeta)
	}
	if a.chat.approvalOwners == nil {
		a.chat.approvalOwners = make(map[string]*chatTask)
	}
	sessionKey := chatSessionKey(projectID, sessionID)
	if a.chat.bySession[sessionKey] != "" {
		a.chat.mu.Unlock()
		cancel()
		return "", errors.New("当前会话已有任务正在运行，请切换会话或将消息加入队列")
	}
	a.chat.tasks[task.id] = task
	a.chat.bySession[sessionKey] = task.id
	a.chat.active = task
	a.chat.mu.Unlock()
	runtime.SetApprovalNotify(func(request agent.ApprovalRequest) {
		a.chat.mu.Lock()
		if a.chat.approvalOwners == nil {
			a.chat.approvalOwners = make(map[string]*chatTask)
		}
		a.chat.approvalOwners[request.ID] = task
		a.chat.mu.Unlock()
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "approval:request", request)
		}
	})
	if registered != nil {
		registered(task.id)
	}

	go a.runChatTask(ctx, task, prompt, attachments)
	return task.id, nil
}

func (a *App) StopChatMessage(taskID string) bool {
	task := a.cancelActiveChatTask(strings.TrimSpace(taskID))
	if task == nil {
		return false
	}
	go a.enforceChatTaskStop(task, chatTaskStopGracePeriod)
	return true
}

// StopSubagent cancels one delegated worker while the parent task and sibling
// workers continue running.
func (a *App) StopSubagent(parentTaskID, subagentTaskID string) bool {
	parentTaskID = strings.TrimSpace(parentTaskID)
	subagentTaskID = strings.TrimSpace(subagentTaskID)
	if subagentTaskID == "" {
		return false
	}
	a.chat.mu.Lock()
	task := a.chat.tasks[parentTaskID]
	if task == nil && a.chat.active != nil && (parentTaskID == "" || a.chat.active.id == parentTaskID) {
		task = a.chat.active
	}
	a.chat.mu.Unlock()
	if task == nil || task.service == nil {
		return false
	}
	return task.service.CancelSubagent(subagentTaskID)
}

func (a *App) GuideChatMessage(taskID, guidanceID, prompt string) (bool, error) {
	return a.GuideChatMessageWithAttachments(taskID, guidanceID, prompt, nil)
}

func (a *App) GuideChatMessageWithAttachments(taskID, guidanceID, prompt string, attachments []agent.ChatAttachment) (bool, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" && len(attachments) == 0 {
		return false, errors.New("引导消息内容不能为空")
	}
	guidanceID = strings.TrimSpace(guidanceID)
	if guidanceID == "" {
		guidanceID = fmt.Sprintf("guidance-%d", time.Now().UnixNano())
	}

	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	task := a.chat.tasks[strings.TrimSpace(taskID)]
	if task == nil && a.chat.active != nil && a.chat.active.id == strings.TrimSpace(taskID) {
		task = a.chat.active
	}
	if task == nil || task.id != strings.TrimSpace(taskID) || !task.acceptingGuidance {
		return false, nil
	}
	task.guidance = append(task.guidance, chatGuidance{
		id:          guidanceID,
		prompt:      prompt,
		attachments: append([]agent.ChatAttachment(nil), attachments...),
	})
	return true, nil
}

func (a *App) GetActiveChatTask() *ChatTaskState {
	a.chat.mu.Lock()
	task := a.chat.active
	if task == nil {
		for _, candidate := range a.chat.tasks {
			task = candidate
			break
		}
	}
	if task == nil {
		a.chat.mu.Unlock()
		return nil
	}
	status := task.status
	a.chat.mu.Unlock()
	return chatTaskState(task, status)
}

func (a *App) GetActiveChatTasks() []*ChatTaskState {
	a.chat.mu.Lock()
	tasks := make([]struct {
		task   *chatTask
		status string
	}, 0, len(a.chat.tasks)+1)
	for _, task := range a.chat.tasks {
		tasks = append(tasks, struct {
			task   *chatTask
			status string
		}{task: task, status: task.status})
	}
	if len(tasks) == 0 && a.chat.active != nil {
		tasks = append(tasks, struct {
			task   *chatTask
			status string
		}{task: a.chat.active, status: a.chat.active.status})
	}
	a.chat.mu.Unlock()
	states := make([]*ChatTaskState, 0, len(tasks))
	for _, entry := range tasks {
		states = append(states, chatTaskState(entry.task, entry.status))
	}
	return states
}

func chatTaskState(task *chatTask, status string) *ChatTaskState {
	if task == nil {
		return nil
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "running"
	}
	state := &ChatTaskState{
		TaskID: task.id, StartedAt: task.startedAt, ProjectID: task.projectID,
		SessionID: task.sessionID, Status: status,
	}
	if task.service != nil {
		if snapshot, ok := task.service.TaskRuntimeSnapshot(); ok {
			state.UpdatedAt = snapshot.UpdatedAt
			state.Message = snapshot.Message
			state.Model = snapshot.Model
			state.Content = snapshot.Content
			state.Reasoning = snapshot.Reasoning
			state.DurationMs = snapshot.DurationMs
			state.Parts = snapshot.Parts
			if state.StartedAt == "" {
				state.StartedAt = snapshot.StartedAt
			}
			if status == "running" && snapshot.Status != "" {
				state.Status = snapshot.Status
			}
		}
	}
	return state
}

func (a *App) requireIdleChat(action string) error {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	if len(a.chat.tasks) > 0 || a.chat.active != nil {
		return fmt.Errorf("对话任务正在运行，停止生成后才能%s", action)
	}
	return nil
}

func (a *App) requireSessionIdleChat(sessionID string) error {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	for _, task := range a.chat.tasks {
		if task != nil && task.sessionID == sessionID {
			return fmt.Errorf("该会话已有对话任务正在运行")
		}
	}
	return nil
}

func (a *App) requireProjectSessionIdleChat(projectID, sessionID string) error {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if taskID := a.chat.bySession[chatSessionKey(projectID, sessionID)]; taskID != "" {
		return fmt.Errorf("该会话已有对话任务正在运行")
	}
	for _, task := range a.chat.tasks {
		if task != nil && task.projectID == projectID && task.sessionID == sessionID {
			return fmt.Errorf("该会话已有对话任务正在运行")
		}
	}
	return nil
}

func chatSessionKey(projectID, sessionID string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(sessionID)
}

func (a *App) runChatTask(ctx context.Context, task *chatTask, prompt string, attachments []agent.ChatAttachment) {
	defer task.markDone()
	generation, _ := task.service.StartTaskRuntimeWithGeneration(task.id, task.startedAt)
	task.setGeneration(generation)
	a.emitChatTaskEvent(ChatTaskEvent{TaskID: task.id, Type: "started", Message: "正在准备上下文"})
	newProgressSink := func(turnGeneration uint64) agent.ChatEventSink {
		return func(progress agent.ChatStreamEvent) {
			a.recordChatTaskProgressForGeneration(task, turnGeneration, progress)
		}
	}
	emitProgress := newProgressSink(generation)

	var result agent.ChatResult
	var err error
	if task.kind == chatTaskKindTeamResume {
		result, err = task.service.ResumePausedTeamTaskWithEvents(ctx, emitProgress)
		if err == nil && ctx.Err() == nil {
			a.completeChatTask(task, ChatTaskEvent{TaskID: task.id, Type: "completed", Model: result.Model, Result: &result})
			return
		}
	} else {
		currentPrompt := prompt
		currentAttachments := attachments
		guidanceTurn := false
		for {
			if guidanceTurn {
				result, err = task.service.SendChatGuidanceWithAttachmentsAndEvents(ctx, currentPrompt, currentAttachments, emitProgress)
			} else {
				result, err = task.service.SendChatMessageWithAttachmentsAndEvents(ctx, currentPrompt, currentAttachments, emitProgress)
			}
			if err != nil || ctx.Err() != nil {
				break
			}

			guidance, ok := a.takeNextChatGuidance(task)
			if !ok {
				a.completeChatTask(task, ChatTaskEvent{TaskID: task.id, Type: "completed", Model: result.Model, Result: &result})
				return
			}

			completedResult := result
			a.emitChatTaskEvent(ChatTaskEvent{
				TaskID:      task.id,
				Type:        "guidance",
				Message:     "已收到引导，正在调整当前任务",
				GuidanceID:  guidance.id,
				Guidance:    guidance.prompt,
				Attachments: append([]agent.ChatAttachment(nil), guidance.attachments...),
				Result:      &completedResult,
			})
			generation, _ = task.service.StartGuidedTaskRuntimeWithGeneration(task.id, "已收到引导，正在调整当前任务", result.Model)
			task.setGeneration(generation)
			emitProgress = newProgressSink(generation)
			currentPrompt = guidance.prompt
			currentAttachments = guidance.attachments
			guidanceTurn = true
		}
	}

	wasCancelled := chatTaskWasCancelled(ctx, err)

	if err != nil {
		if wasCancelled {
			a.completeChatTask(task, ChatTaskEvent{
				TaskID: task.id, Type: "cancelled", Message: "已停止生成",
				Model: result.Model, Result: &result,
			})
			return
		}
		a.completeChatTask(task, ChatTaskEvent{TaskID: task.id, Type: "failed", Message: err.Error(), Model: result.Model, Result: &result})
		return
	}
}

func (task *chatTask) markDone() {
	if task == nil || task.done == nil {
		return
	}
	task.doneOnce.Do(func() { close(task.done) })
}

func (task *chatTask) setGeneration(generation uint64) {
	if task == nil {
		return
	}
	task.progressMu.Lock()
	task.generation = generation
	task.progressMu.Unlock()
}

func (task *chatTask) currentGeneration() uint64 {
	if task == nil {
		return 0
	}
	task.progressMu.Lock()
	defer task.progressMu.Unlock()
	return task.generation
}

func (a *App) completeChatTask(task *chatTask, event ChatTaskEvent) {
	if task == nil {
		return
	}
	task.terminalOnce.Do(func() {
		// A provider, tool, or detached worker can emit after cancellation or
		// completion. Close the application-side gate before durable finalization
		// so a late event cannot revive the UI or overwrite the final snapshot.
		task.progressMu.Lock()
		task.terminal = true
		generation := task.generation
		task.progressMu.Unlock()
		result := agent.ChatResult{}
		if event.Result != nil {
			result = *event.Result
		}
		if task.service != nil {
			if generation != 0 {
				_, _ = task.service.FinishTaskRuntimeForGeneration(task.id, generation, event.Type, event.Message, result)
			} else {
				_ = task.service.FinishTaskRuntime(task.id, event.Type, event.Message, result)
			}
		}
		if task.cancel != nil {
			task.cancel()
		}
		// Keep the task registered until its durable state has been reloaded.
		// Otherwise a successor task in the same session can start while this
		// task's older snapshot is still being applied and overwrite it.
		a.reloadCompletedChatTask(task)
		a.finishChatTask(task)
		a.emitChatTaskEvent(event)
	})
}

func (a *App) recordChatTaskProgress(task *chatTask, progress agent.ChatStreamEvent) {
	a.recordChatTaskProgressForGeneration(task, task.currentGeneration(), progress)
}

func (a *App) recordChatTaskProgressForGeneration(task *chatTask, generation uint64, progress agent.ChatStreamEvent) {
	if task == nil {
		return
	}
	task.progressMu.Lock()
	defer task.progressMu.Unlock()
	if task.terminal {
		return
	}
	if task.service != nil {
		if generation != 0 {
			accepted, _ := task.service.RecordTaskStreamEventForGeneration(task.id, generation, progress)
			if !accepted {
				return
			}
		} else {
			_ = task.service.RecordTaskStreamEvent(task.id, progress)
		}
	}
	a.emitChatTaskEvent(ChatTaskEvent{
		TaskID:      task.id,
		Type:        progress.Type,
		Delta:       progress.Delta,
		Message:     progress.Message,
		Model:       progress.Model,
		ToolName:    progress.ToolName,
		ToolCallID:  progress.ToolCallID,
		ToolInput:   progress.ToolInput,
		Status:      progress.Status,
		Usage:       progress.Usage,
		UsageState:  progress.UsageState,
		Progress:    progress.Progress,
		Parts:       progress.Parts,
		Compression: progress.Compression,
		Team:        progress.Team,
	})
}

func (a *App) enforceChatTaskStop(task *chatTask, grace time.Duration) {
	if task == nil || task.done == nil {
		return
	}
	if grace <= 0 {
		grace = chatTaskStopGracePeriod
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-task.done:
		return
	case <-timer.C:
		a.completeChatTask(task, ChatTaskEvent{
			TaskID:  task.id,
			Type:    "cancelled",
			Message: "任务未在取消期限内退出，已强制结束并转入后台清理。",
			Forced:  true,
		})
	}
}

func (a *App) reloadCompletedChatTask(task *chatTask) {
	if a.service == nil || task == nil {
		return
	}
	_, _, _ = a.service.ReloadProjectSessionIfActive(task.projectID, task.sessionID)
}

func (a *App) takeNextChatGuidance(task *chatTask) (chatGuidance, bool) {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	tracked := a.chat.tasks[task.id]
	if tracked == nil && a.chat.active == task {
		tracked = task
	}
	if tracked != task || !task.acceptingGuidance {
		removeChatTaskLocked(&a.chat, task)
		return chatGuidance{}, false
	}
	if len(task.guidance) == 0 {
		task.acceptingGuidance = false
		removeChatTaskLocked(&a.chat, task)
		return chatGuidance{}, false
	}
	guidance := task.guidance[0]
	task.guidance = task.guidance[1:]
	return guidance, true
}

func (a *App) finishChatTask(task *chatTask) {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	task.acceptingGuidance = false
	removeChatTaskLocked(&a.chat, task)
}

func chatTaskWasCancelled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
}

func (a *App) cancelActiveChatTask(taskID string) *chatTask {
	a.chat.mu.Lock()
	task := a.chat.tasks[strings.TrimSpace(taskID)]
	if task == nil && a.chat.active != nil && (taskID == "" || a.chat.active.id == taskID) {
		task = a.chat.active
	}
	if task == nil || (taskID != "" && task.id != taskID) {
		a.chat.mu.Unlock()
		return nil
	}
	task.acceptingGuidance = false
	task.status = "cancelled"
	a.chat.mu.Unlock()
	if task.service != nil {
		generation := task.currentGeneration()
		if generation != 0 {
			_, _ = task.service.MarkTaskRuntimeCancelling(task.id, generation, "正在停止任务")
		}
	}
	if task.cancel != nil {
		task.cancel()
	}
	return task
}

func (a *App) cancelAllChatTasks() {
	a.chat.mu.Lock()
	seen := make(map[string]bool)
	tasks := make([]*chatTask, 0, len(a.chat.tasks)+1)
	for _, task := range a.chat.tasks {
		if task == nil || seen[task.id] {
			continue
		}
		seen[task.id] = true
		task.acceptingGuidance = false
		task.status = "cancelled"
		tasks = append(tasks, task)
	}
	if a.chat.active != nil && !seen[a.chat.active.id] {
		a.chat.active.acceptingGuidance = false
		a.chat.active.status = "cancelled"
		tasks = append(tasks, a.chat.active)
	}
	a.chat.mu.Unlock()

	for _, task := range tasks {
		if task.service != nil {
			generation := task.currentGeneration()
			if generation != 0 {
				_, _ = task.service.MarkTaskRuntimeCancelling(task.id, generation, "正在停止任务")
			}
		}
		if task.cancel != nil {
			task.cancel()
		}
	}
}

func removeChatTaskLocked(runner *chatTaskRunner, task *chatTask) {
	if task == nil {
		return
	}
	if runner.meta == nil {
		runner.meta = make(map[string]chatTaskMeta)
	}
	runner.meta[task.id] = chatTaskMeta{projectID: task.projectID, sessionID: task.sessionID}
	if runner.tasks != nil {
		delete(runner.tasks, task.id)
	}
	key := chatSessionKey(task.projectID, task.sessionID)
	if runner.bySession != nil && runner.bySession[key] == task.id {
		delete(runner.bySession, key)
	}
	for requestID, owner := range runner.approvalOwners {
		if owner == task {
			delete(runner.approvalOwners, requestID)
		}
	}
	if runner.active == task {
		runner.active = nil
		for _, candidate := range runner.tasks {
			runner.active = candidate
			break
		}
	}
}

func (a *App) approvalService(requestID string) *agent.Service {
	a.chat.mu.Lock()
	defer a.chat.mu.Unlock()
	task := a.chat.approvalOwners[strings.TrimSpace(requestID)]
	if task == nil {
		return nil
	}
	delete(a.chat.approvalOwners, strings.TrimSpace(requestID))
	return task.service
}

func (a *App) emitChatTaskEvent(event ChatTaskEvent) {
	if event.TaskID != "" {
		a.chat.mu.Lock()
		if task := a.chat.tasks[event.TaskID]; task != nil {
			if status := chatTaskStatusFromEvent(event); status != "" {
				task.status = status
			}
			if event.ProjectID == "" {
				event.ProjectID = task.projectID
			}
			if event.SessionID == "" {
				event.SessionID = task.sessionID
			}
		} else if meta, ok := a.chat.meta[event.TaskID]; ok {
			if event.ProjectID == "" {
				event.ProjectID = meta.projectID
			}
			if event.SessionID == "" {
				event.SessionID = meta.sessionID
			}
		}
		a.chat.mu.Unlock()
	}
	if a.automations != nil {
		switch event.Type {
		case "completed":
			a.automations.CompleteChatTask(event.TaskID, "completed", "Agent 已完成自动化任务")
		case "failed":
			a.automations.CompleteChatTask(event.TaskID, "failed", event.Message)
		case "cancelled":
			a.automations.CompleteChatTask(event.TaskID, "cancelled", event.Message)
		}
	}
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, "chat:task", event)
	}
}

func chatTaskStatusFromEvent(event ChatTaskEvent) string {
	switch event.Type {
	case "failed", "cancelled", "completed":
		return event.Type
	case "status", "started":
		if status := strings.TrimSpace(event.Status); status != "" {
			return status
		}
		return "running"
	case "heartbeat":
		return ""
	case "tool":
		switch event.Status {
		case "waiting", "retrying":
			return event.Status
		default:
			return "running"
		}
	case "context_compression", "delta", "reasoning", "provider_notice", "subagent", "progress", "team", "guidance":
		return "running"
	default:
		return ""
	}
}
