package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const schemaVersion = 1

type Schedule struct {
	Kind            string `json:"kind"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
	DailyTime       string `json:"dailyTime,omitempty"`
}

type Run struct {
	Status     string `json:"status"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Message    string `json:"message,omitempty"`
	ChatTaskID string `json:"chatTaskId,omitempty"`
}

type Task struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Prompt       string   `json:"prompt"`
	ProjectID    string   `json:"projectId"`
	SessionID    string   `json:"sessionId"`
	ProviderID   string   `json:"providerId,omitempty"`
	ModelID      string   `json:"modelId,omitempty"`
	Schedule     Schedule `json:"schedule"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	NextRunAt    string   `json:"nextRunAt,omitempty"`
	LastRun      *Run     `json:"lastRun,omitempty"`
	RunCount     int      `json:"runCount"`
	FailureCount int      `json:"failureCount"`
}

type State struct {
	Tasks     []Task `json:"tasks"`
	Running   bool   `json:"running"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type persistedState struct {
	SchemaVersion int    `json:"schemaVersion"`
	Tasks         []Task `json:"tasks"`
}

type Runner func(Task) (string, error)

type Service struct {
	mu        sync.Mutex
	path      string
	tasks     []Task
	runner    Runner
	notify    func(State)
	cancel    context.CancelFunc
	wake      chan struct{}
	running   bool
	updatedAt string
}

func New(path string) (*Service, error) {
	service := &Service{path: strings.TrimSpace(path), wake: make(chan struct{}, 1)}
	if err := service.load(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) SetRunner(runner Runner) {
	s.mu.Lock()
	s.runner = runner
	s.mu.Unlock()
}

func (s *Service) SetNotify(notify func(State)) {
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()
}

func (s *Service) Start(parent context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.running = true
	state, notify := s.snapshotLocked(), s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
	go s.loop(ctx)
}

func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.running = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) Save(task Task) (State, error) {
	now := time.Now()
	task = normalizeTask(task)
	if err := validateTask(task); err != nil {
		return s.State(), err
	}

	s.mu.Lock()
	index := s.taskIndexLocked(task.ID)
	if index < 0 {
		task.ID = fmt.Sprintf("auto-%d", now.UnixNano())
		task.CreatedAt = now.UTC().Format(time.RFC3339Nano)
		task.RunCount = 0
		task.FailureCount = 0
		task.LastRun = nil
		s.tasks = append(s.tasks, task)
	} else {
		current := s.tasks[index]
		task.CreatedAt = current.CreatedAt
		task.LastRun = cloneRun(current.LastRun)
		task.RunCount = current.RunCount
		task.FailureCount = current.FailureCount
		s.tasks[index] = task
	}
	task.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if task.Enabled {
		task.NextRunAt = nextRun(task.Schedule, now).UTC().Format(time.RFC3339Nano)
	} else {
		task.NextRunAt = ""
	}
	index = s.taskIndexLocked(task.ID)
	s.tasks[index] = task
	s.sortLocked()
	state, err := s.persistAndSnapshotLocked(now)
	notify := s.notify
	s.mu.Unlock()
	if err != nil {
		return state, err
	}
	if notify != nil {
		notify(state)
	}
	s.signalWake()
	return state, nil
}

func (s *Service) Delete(taskID string) (State, error) {
	s.mu.Lock()
	index := s.taskIndexLocked(taskID)
	if index < 0 {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, fmt.Errorf("自动化任务不存在: %s", taskID)
	}
	if s.tasks[index].LastRun != nil && s.tasks[index].LastRun.Status == "running" {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, errors.New("任务正在运行，完成后才能删除")
	}
	s.tasks = append(s.tasks[:index], s.tasks[index+1:]...)
	state, err := s.persistAndSnapshotLocked(time.Now())
	notify := s.notify
	s.mu.Unlock()
	if err == nil && notify != nil {
		notify(state)
	}
	return state, err
}

func (s *Service) SetEnabled(taskID string, enabled bool) (State, error) {
	s.mu.Lock()
	index := s.taskIndexLocked(taskID)
	if index < 0 {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, fmt.Errorf("自动化任务不存在: %s", taskID)
	}
	now := time.Now()
	s.tasks[index].Enabled = enabled
	s.tasks[index].UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if enabled {
		s.tasks[index].NextRunAt = nextRun(s.tasks[index].Schedule, now).UTC().Format(time.RFC3339Nano)
	} else {
		s.tasks[index].NextRunAt = ""
	}
	state, err := s.persistAndSnapshotLocked(now)
	notify := s.notify
	s.mu.Unlock()
	if err == nil && notify != nil {
		notify(state)
	}
	s.signalWake()
	return state, err
}

func (s *Service) RunNow(taskID string) (State, error) {
	return s.startTask(strings.TrimSpace(taskID), time.Now())
}

func (s *Service) MarkStopping(taskID string) (State, error) {
	s.mu.Lock()
	index := s.taskIndexLocked(taskID)
	if index < 0 {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, fmt.Errorf("自动化任务不存在: %s", taskID)
	}
	run := s.tasks[index].LastRun
	if run == nil || run.Status != "running" || run.ChatTaskID == "" {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, errors.New("自动化任务当前没有可停止的 Agent")
	}
	run = cloneRun(run)
	run.Message = "正在停止 Agent"
	s.tasks[index].LastRun = run
	state, err := s.persistAndSnapshotLocked(time.Now())
	notify := s.notify
	s.mu.Unlock()
	if err == nil && notify != nil {
		notify(state)
	}
	return state, err
}

// AttachChatTask records the Agent task ID before its goroutine starts. This
// guarantees that an immediate Agent failure can still be attributed to the
// automation that launched it.
func (s *Service) AttachChatTask(taskID, chatTaskID string) bool {
	taskID = strings.TrimSpace(taskID)
	chatTaskID = strings.TrimSpace(chatTaskID)
	if taskID == "" || chatTaskID == "" {
		return false
	}
	s.mu.Lock()
	index := s.taskIndexLocked(taskID)
	if index < 0 || s.tasks[index].LastRun == nil {
		s.mu.Unlock()
		return false
	}
	run := cloneRun(s.tasks[index].LastRun)
	if run.Status != "starting" && run.Status != "running" {
		s.mu.Unlock()
		return false
	}
	run.Status = "running"
	run.ChatTaskID = chatTaskID
	run.Message = "Agent 正在执行任务"
	s.tasks[index].LastRun = run
	state, _ := s.persistAndSnapshotLocked(time.Now())
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
	return true
}

func (s *Service) CompleteChatTask(chatTaskID, status, message string) bool {
	chatTaskID = strings.TrimSpace(chatTaskID)
	if chatTaskID == "" {
		return false
	}
	s.mu.Lock()
	index := -1
	for candidate := range s.tasks {
		run := s.tasks[candidate].LastRun
		if run != nil && run.Status == "running" && run.ChatTaskID == chatTaskID {
			index = candidate
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return false
	}
	status = normalizeRunStatus(status)
	run := cloneRun(s.tasks[index].LastRun)
	run.Status = status
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	run.Message = strings.TrimSpace(message)
	s.tasks[index].LastRun = run
	if status == "failed" || status == "cancelled" {
		s.tasks[index].FailureCount++
	}
	state, _ := s.persistAndSnapshotLocked(time.Now())
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
	return true
}

func (s *Service) loop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.cancel = nil
		s.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(time.Now())
		case <-s.wake:
			s.runDue(time.Now())
		}
	}
}

func (s *Service) runDue(now time.Time) {
	s.mu.Lock()
	ids := make([]string, 0)
	for _, task := range s.tasks {
		if !task.Enabled || task.NextRunAt == "" || (task.LastRun != nil && task.LastRun.Status == "running") {
			continue
		}
		due, err := time.Parse(time.RFC3339Nano, task.NextRunAt)
		if err == nil && !due.After(now) {
			ids = append(ids, task.ID)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		_, _ = s.startTask(id, now)
	}
}

func (s *Service) startTask(taskID string, now time.Time) (State, error) {
	s.mu.Lock()
	index := s.taskIndexLocked(taskID)
	if index < 0 {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, fmt.Errorf("自动化任务不存在: %s", taskID)
	}
	if s.tasks[index].LastRun != nil && s.tasks[index].LastRun.Status == "running" {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, errors.New("自动化任务已经在运行")
	}
	runner := s.runner
	if runner == nil {
		state := s.snapshotLocked()
		s.mu.Unlock()
		return state, errors.New("自动化任务运行器尚未初始化")
	}
	s.tasks[index].RunCount++
	s.tasks[index].LastRun = &Run{Status: "starting", StartedAt: now.UTC().Format(time.RFC3339Nano), Message: "正在启动 Agent"}
	if s.tasks[index].Enabled {
		s.tasks[index].NextRunAt = nextRun(s.tasks[index].Schedule, now).UTC().Format(time.RFC3339Nano)
	}
	task := cloneTask(s.tasks[index])
	state, err := s.persistAndSnapshotLocked(now)
	notify := s.notify
	s.mu.Unlock()
	if err != nil {
		return state, err
	}
	if notify != nil {
		notify(state)
	}

	chatTaskID, runErr := runner(task)
	s.mu.Lock()
	index = s.taskIndexLocked(taskID)
	if index < 0 || s.tasks[index].LastRun == nil {
		state = s.snapshotLocked()
		s.mu.Unlock()
		return state, runErr
	}
	run := cloneRun(s.tasks[index].LastRun)
	if run.Status != "starting" && run.Status != "running" {
		state = s.snapshotLocked()
		s.mu.Unlock()
		return state, runErr
	}
	if runErr != nil {
		run.Status = "failed"
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		run.Message = runErr.Error()
		s.tasks[index].FailureCount++
	} else {
		run.Status = "running"
		run.ChatTaskID = chatTaskID
		run.Message = "Agent 正在执行任务"
	}
	s.tasks[index].LastRun = run
	state, persistErr := s.persistAndSnapshotLocked(time.Now())
	notify = s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
	if runErr != nil {
		return state, runErr
	}
	return state, persistErr
}

func (s *Service) load() error {
	if s.path == "" {
		return nil
	}
	payload, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var persisted persistedState
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return fmt.Errorf("读取自动化任务: %w", err)
	}
	now := time.Now()
	for _, task := range persisted.Tasks {
		task = normalizeTask(task)
		if validateTask(task) != nil || strings.TrimSpace(task.ID) == "" {
			continue
		}
		if task.LastRun != nil && (task.LastRun.Status == "running" || task.LastRun.Status == "starting") {
			task.LastRun.Status = "interrupted"
			task.LastRun.FinishedAt = now.UTC().Format(time.RFC3339Nano)
			task.LastRun.Message = "应用上次退出时任务仍在运行"
			task.FailureCount++
		}
		if task.Enabled && task.NextRunAt == "" {
			task.NextRunAt = nextRun(task.Schedule, now).UTC().Format(time.RFC3339Nano)
		}
		s.tasks = append(s.tasks, task)
	}
	s.sortLocked()
	return nil
}

func (s *Service) persistAndSnapshotLocked(now time.Time) (State, error) {
	s.updatedAt = now.UTC().Format(time.RFC3339Nano)
	state := s.snapshotLocked()
	if s.path == "" {
		return state, nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return state, err
	}
	payload, err := json.MarshalIndent(persistedState{SchemaVersion: schemaVersion, Tasks: s.tasks}, "", "  ")
	if err != nil {
		return state, err
	}
	return state, tools.WriteBytesAtomic(s.path, payload, 0o600)
}

func (s *Service) snapshotLocked() State {
	tasks := make([]Task, len(s.tasks))
	for index, task := range s.tasks {
		tasks[index] = cloneTask(task)
	}
	return State{Tasks: tasks, Running: s.running, UpdatedAt: s.updatedAt}
}

func (s *Service) taskIndexLocked(taskID string) int {
	for index := range s.tasks {
		if s.tasks[index].ID == strings.TrimSpace(taskID) {
			return index
		}
	}
	return -1
}

func (s *Service) sortLocked() {
	sort.SliceStable(s.tasks, func(left, right int) bool {
		if s.tasks[left].Enabled != s.tasks[right].Enabled {
			return s.tasks[left].Enabled
		}
		return s.tasks[left].CreatedAt > s.tasks[right].CreatedAt
	})
}

func (s *Service) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func normalizeTask(task Task) Task {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.ProjectID = strings.TrimSpace(task.ProjectID)
	task.SessionID = strings.TrimSpace(task.SessionID)
	task.ProviderID = strings.TrimSpace(task.ProviderID)
	task.ModelID = strings.TrimSpace(task.ModelID)
	task.Schedule.Kind = strings.ToLower(strings.TrimSpace(task.Schedule.Kind))
	if task.Schedule.Kind == "" {
		task.Schedule.Kind = "interval"
	}
	if task.Schedule.IntervalMinutes < 1 {
		task.Schedule.IntervalMinutes = 60
	}
	if task.Schedule.IntervalMinutes > 525600 {
		task.Schedule.IntervalMinutes = 525600
	}
	task.Schedule.DailyTime = strings.TrimSpace(task.Schedule.DailyTime)
	if task.Schedule.DailyTime == "" {
		task.Schedule.DailyTime = "09:00"
	}
	return task
}

func validateTask(task Task) error {
	if task.Name == "" {
		return errors.New("任务名称不能为空")
	}
	if len([]rune(task.Name)) > 120 {
		return errors.New("任务名称不能超过 120 个字符")
	}
	if task.Prompt == "" {
		return errors.New("任务内容不能为空")
	}
	if task.ProjectID == "" || task.SessionID == "" {
		return errors.New("自动化任务必须绑定项目和会话")
	}
	if task.Schedule.Kind != "interval" && task.Schedule.Kind != "daily" {
		return errors.New("自动化计划仅支持间隔或每日")
	}
	if task.Schedule.Kind == "daily" {
		if _, err := time.Parse("15:04", task.Schedule.DailyTime); err != nil {
			return errors.New("每日执行时间必须是 HH:MM")
		}
	}
	return nil
}

func nextRun(schedule Schedule, now time.Time) time.Time {
	if schedule.Kind == "daily" {
		clock, err := time.Parse("15:04", schedule.DailyTime)
		if err == nil {
			candidate := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
			if !candidate.After(now) {
				candidate = candidate.AddDate(0, 0, 1)
			}
			return candidate
		}
	}
	minutes := schedule.IntervalMinutes
	if minutes < 1 {
		minutes = 60
	}
	return now.Add(time.Duration(minutes) * time.Minute)
}

func normalizeRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	default:
		return "failed"
	}
}

func cloneTask(task Task) Task {
	copy := task
	copy.LastRun = cloneRun(task.LastRun)
	return copy
}

func cloneRun(run *Run) *Run {
	if run == nil {
		return nil
	}
	copy := *run
	return &copy
}
