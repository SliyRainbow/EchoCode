package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	defaultMaxConcurrentSubagents = 4
	maxConcurrentSubagents        = 16
	maxSubagentTimelineSteps      = 10
	maxSubagentActivities         = 120
	maxSubagentOutputRunes        = 96_000
	subagentCancellationJoinGrace = 750 * time.Millisecond
)

const (
	subagentActivityOverflowID = "echocode-subagent-activity-overflow"
	subagentActivityOverflow   = "较早的活动已折叠；后续工具活动仍在继续记录。"
	subagentStepOverflow       = "更多工具活动见详细记录"
)

const (
	subagentExplore   = "explore"
	subagentReview    = "review"
	subagentImplement = "implement"
)

var subagentSequence atomic.Uint64

func nextSubagentGeneration() uint64 {
	candidate := uint64(time.Now().UnixNano())
	for {
		current := subagentSequence.Load()
		if candidate <= current {
			candidate = current + 1
		}
		if subagentSequence.CompareAndSwap(current, candidate) {
			return candidate
		}
	}
}

type subagentExecutionScope struct {
	BaseRequest  protocol.ChatRequest
	PrimaryRoute chatRoute
}

type subagentExecutionScopeKey struct{}

func withSubagentExecutionScope(ctx context.Context, scope subagentExecutionScope) context.Context {
	return context.WithValue(ctx, subagentExecutionScopeKey{}, scope)
}

func subagentExecutionScopeFrom(ctx context.Context) (subagentExecutionScope, bool) {
	scope, ok := ctx.Value(subagentExecutionScopeKey{}).(subagentExecutionScope)
	return scope, ok
}

func (s *Service) registerSubagent(parent context.Context, part tools.ResultPart) (context.Context, *subagentControl) {
	projectID, sessionID := s.subagentRegistryIdentity()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := normalizeSubagentTaskRecord(SubagentTaskRecord{
		Version: subagentTaskRegistryVersion, TaskID: part.TaskID,
		ProjectID: projectID, SessionID: sessionID, Generation: nextSubagentGeneration(),
		AgentType: part.AgentType, Label: part.Label, Status: part.Status,
		CreatedAt: now, UpdatedAt: now, RecoveryState: "active",
	})
	ctx, control := s.registerSubagentWithRecord(parent, part, record)
	_ = s.persistSubagentControl(control)
	return ctx, control
}

func (s *Service) registerSubagentWithRecord(parent context.Context, part tools.ResultPart, record SubagentTaskRecord) (context.Context, *subagentControl) {
	ctx, cancel := context.WithCancel(parent)
	control := &subagentControl{
		taskID:            part.TaskID,
		cancel:            cancel,
		done:              make(chan struct{}),
		latest:            cloneSubagentPart(part),
		taskRecord:        normalizeSubagentTaskRecord(record),
		registryLastWrite: time.Now().UTC(),
	}
	s.subagentMu.Lock()
	if s.subagents == nil {
		s.subagents = make(map[string]*subagentControl)
	}
	s.subagents[part.TaskID] = control
	s.subagentMu.Unlock()
	return ctx, control
}

// CancelSubagent stops one delegated worker without cancelling its siblings or
// the coordinating parent turn.
func (s *Service) CancelSubagent(taskID string) bool {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	s.subagentMu.Lock()
	control := s.subagents[taskID]
	s.subagentMu.Unlock()
	if control == nil || control.cancel == nil {
		return false
	}
	if control.markCancelRequested() {
		_ = s.persistSubagentControl(control)
	}
	control.cancel()
	return true
}

type delegateTaskArguments struct {
	Tasks []delegateTaskSpec `json:"tasks"`
}

type delegateTaskSpec struct {
	Label      string `json:"label"`
	Task       string `json:"task"`
	AgentType  string `json:"agentType"`
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
}

type delegatedTaskResult struct {
	part      tools.ResultPart
	artifacts []tools.ResultPart
	usage     []protocol.TokenUsage
	route     chatRoute
}

type subagentControl struct {
	mu                sync.RWMutex
	taskID            string
	cancel            context.CancelFunc
	done              chan struct{}
	doneOnce          sync.Once
	latest            tools.ResultPart
	result            delegatedTaskResult
	finished          bool
	collected         bool
	taskRecord        SubagentTaskRecord
	registryLastWrite time.Time
}

func (c *subagentControl) update(part tools.ResultPart) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return false
	}
	c.latest = cloneSubagentPart(part)
	due := c.updateTaskRecordFromPartLocked(part, false)
	c.mu.Unlock()
	return due
}

func (c *subagentControl) finish(result delegatedTaskResult) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return false
	}
	c.result = cloneDelegatedTaskResult(result)
	c.latest = cloneSubagentPart(result.part)
	c.finished = true
	c.finishTaskRecordLocked(result)
	c.mu.Unlock()
	c.doneOnce.Do(func() { close(c.done) })
	return true
}

func (c *subagentControl) snapshot() (tools.ResultPart, delegatedTaskResult, bool, bool) {
	if c == nil {
		return tools.ResultPart{}, delegatedTaskResult{}, false, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSubagentPart(c.latest), cloneDelegatedTaskResult(c.result), c.finished, c.collected
}

func (c *subagentControl) collect() (delegatedTaskResult, bool, bool) {
	if c == nil {
		return delegatedTaskResult{}, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finished {
		return delegatedTaskResult{}, false, false
	}
	newlyCollected := !c.collected
	c.collected = true
	c.taskRecord.Collected = true
	c.taskRecord.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return cloneDelegatedTaskResult(c.result), true, newlyCollected
}

func (c *subagentControl) stateFlags() (finished, collected bool) {
	if c == nil {
		return false, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.finished, c.collected
}

func cloneDelegatedTaskResult(result delegatedTaskResult) delegatedTaskResult {
	result.part = cloneSubagentPart(result.part)
	result.artifacts = append([]tools.ResultPart(nil), result.artifacts...)
	result.usage = append([]protocol.TokenUsage(nil), result.usage...)
	return result
}

// DelegateTaskTool starts independent workers in the background and returns
// immediately. Worker registries deliberately omit coordinator tools, so
// delegation cannot recurse.
type DelegateTaskTool struct {
	Service  *Service
	ReadOnly bool
}

func (DelegateTaskTool) Name() string { return "delegate_task" }

func (t DelegateTaskTool) concurrencyLimit() int {
	if t.Service == nil {
		return defaultMaxConcurrentSubagents
	}
	return t.Service.subagentConcurrencyLimit()
}

// RetainsTaskContext reports that workers started by this tool intentionally
// outlive Execute and remain bound to the parent chat task instead.
func (DelegateTaskTool) RetainsTaskContext() bool { return true }

func (t DelegateTaskTool) Description() string {
	if t.ReadOnly {
		return fmt.Sprintf("将 1-%d 个彼此独立的只读子任务同时放到后台执行并立即返回任务 ID。当前权限仅允许 explore/review，不能委派 implement。启动后主 Agent 应继续自己的独立工作，临近最终综合时再调用 await_subagents。此数值只限制并行工作者，不限制本轮工具调用次数。", t.concurrencyLimit())
	}
	return fmt.Sprintf("将 1-%d 个彼此独立的子任务同时放到后台执行并立即返回任务 ID。explore/review 仅可读取；implement 可修改工作区并继续遵守当前沙箱与审批规则。启动后主 Agent 应继续自己的独立工作，临近最终综合时再调用 await_subagents。此数值只限制并行工作者，不限制本轮工具调用次数。多个 implement 以及主 Agent 的写入范围必须互不重叠。", t.concurrencyLimit())
}

func (t DelegateTaskTool) InputSchema() map[string]any {
	limit := t.concurrencyLimit()
	agentTypes := []string{subagentExplore, subagentReview}
	agentTypeDescription := "explore=只读探索，review=只读审阅。当前权限不允许 implement。"
	taskDescription := "会同时启动的彼此独立只读子任务；需要前后依赖的工作应分多次调用。"
	if !t.ReadOnly {
		agentTypes = append(agentTypes, subagentImplement)
		agentTypeDescription = "explore=只读探索，review=只读审阅，implement=允许修改。"
		taskDescription = "会同时启动的彼此独立子任务；implement 的文件范围不得重叠，需要前后依赖的工作应分多次调用。"
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    limit,
				"description": taskDescription,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"label": map[string]any{
							"type":        "string",
							"description": "用于界面展示的简短任务名。",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "完整、可独立执行的目标与交付要求。",
						},
						"agentType": map[string]any{
							"type":        "string",
							"enum":        agentTypes,
							"description": agentTypeDescription,
						},
						"providerId": map[string]any{
							"type":        "string",
							"description": "可选；留空时跟随主 Agent 的供应商。",
						},
						"model": map[string]any{
							"type":        "string",
							"description": "可选；留空时跟随所选供应商的当前模型。",
						},
					},
					"required": []string{"label", "task", "agentType"},
				},
			},
		},
		"required": []string{"tasks"},
	}
}

func (t DelegateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.Service == nil {
		return delegatedTaskError("子代理执行器未初始化"), nil
	}
	scope, ok := subagentExecutionScopeFrom(ctx)
	if !ok {
		return delegatedTaskError("当前调用缺少主 Agent 执行上下文"), nil
	}

	var args delegateTaskArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return delegatedTaskError("子代理参数无效: " + err.Error()), nil
	}
	limit := t.concurrencyLimit()
	specs, err := normalizeDelegatedTaskSpecs(args.Tasks, limit)
	if err != nil {
		return delegatedTaskError(err.Error()), nil
	}
	if t.ReadOnly {
		for index, spec := range specs {
			if spec.AgentType == subagentImplement {
				return delegatedTaskError(fmt.Sprintf("第 %d 个子任务请求 implement，但当前工作区是只读权限；请改用 explore/review 或调整权限后重试", index+1)), nil
			}
		}
	}
	if active := t.Service.activeSubagentCount(); active+len(specs) > limit {
		return delegatedTaskError(fmt.Sprintf("当前已有 %d 个子代理运行中，并行子代理上限为 %d；请先继续主任务或收集已有结果。", active, limit)), nil
	}

	parts := make([]tools.ResultPart, len(specs))
	workerContexts := make([]context.Context, len(specs))
	controls := make([]*subagentControl, len(specs))
	generation := nextSubagentGeneration()
	callID := fmt.Sprintf("subagent-%d-%d", time.Now().UnixNano(), generation)
	var emitMu sync.Mutex
	emitProgress := func(part tools.ResultPart) {
		emitMu.Lock()
		tools.EmitProgress(ctx, cloneSubagentPart(part))
		emitMu.Unlock()
	}

	for index, spec := range specs {
		parts[index] = tools.ResultPart{
			Kind:      tools.PartSubagent,
			TaskID:    fmt.Sprintf("%s-%d", callID, index+1),
			AgentType: spec.AgentType,
			Label:     spec.Label,
			Status:    "pending",
			Summary:   "等待调度",
		}
	}
	if ctx.Err() != nil {
		records := make([]SubagentTaskRecord, len(parts))
		for index := range parts {
			parts[index].Status = "cancelled"
			parts[index].Summary = "任务在开始前已停止"
			parts[index].CurrentAction = "已停止"
			parts[index].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			records[index] = t.Service.newSubagentTaskRecord(scope, specs[index], parts[index], generation)
			emitProgress(parts[index])
		}
		_ = t.Service.persistSubagentTaskRecords(records)
		return tools.Result{
			Summary: "父任务已停止，子代理未启动。",
			Parts:   parts,
			IsError: true,
		}, nil
	}
	records := make([]SubagentTaskRecord, len(parts))
	for index := range parts {
		records[index] = t.Service.newSubagentTaskRecord(scope, specs[index], parts[index], generation)
	}
	if err := t.Service.persistSubagentTaskRecords(records); err != nil {
		return delegatedTaskError("保存子代理任务登记失败: " + err.Error()), nil
	}
	for index := range parts {
		workerContexts[index], controls[index] = t.Service.registerSubagentWithRecord(ctx, parts[index], records[index])
		emitProgress(parts[index])
	}

	for index, spec := range specs {
		// The caller owns the returned Parts and may trim or annotate them as
		// soon as Execute returns. Workers must not read that shared slice.
		go func(index int, spec delegateTaskSpec, initial tools.ResultPart) {
			control := controls[index]
			emit := func(part tools.ResultPart) {
				part = cloneSubagentPart(part)
				if control.update(part) {
					_ = t.Service.persistSubagentControl(control)
				}
				emitProgress(part)
			}
			result := t.Service.runDelegatedTask(workerContexts[index], scope, spec, initial, emit)
			if control.finish(result) {
				_ = t.Service.persistSubagentControl(control)
			}
		}(index, spec, cloneSubagentPart(parts[index]))
	}
	return tools.Result{
		Summary: delegatedTaskStartSummary(parts),
		Parts:   parts,
	}, nil
}

type awaitSubagentsArguments struct {
	TaskIDs []string `json:"taskIds,omitempty"`
	Wait    *bool    `json:"wait,omitempty"`
}

// AwaitSubagentsTool collects background worker results near the synthesis
// phase. Keeping it separate from delegate_task lets the primary Agent make
// progress while children are still running.
type AwaitSubagentsTool struct {
	Service *Service
}

func (AwaitSubagentsTool) Name() string { return "await_subagents" }

func (AwaitSubagentsTool) Description() string {
	return "查询或等待后台子代理并收集最终结果。默认只查询当前状态而不阻塞主 Agent；仅在最终综合确实依赖子代理结果时显式传 wait=true。"
}

func (AwaitSubagentsTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"taskIds": map[string]any{
				"type":        "array",
				"description": "可选；指定要收集的子代理任务 ID。留空时处理当前轮次所有尚未收集的任务。",
				"items":       map[string]any{"type": "string"},
			},
			"wait": map[string]any{
				"type":        "boolean",
				"description": "true=等待任务结束；false=仅返回当前状态（默认）。",
				"default":     false,
			},
		},
	}
}

func (t AwaitSubagentsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.Service == nil {
		return delegatedTaskError("子代理执行器未初始化"), nil
	}
	var args awaitSubagentsArguments
	if len(strings.TrimSpace(string(rawArgs))) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return delegatedTaskError("收集子代理参数无效: " + err.Error()), nil
		}
	}
	taskIDs := normalizeSubagentTaskIDs(args.TaskIDs)
	// Polling is the safe default. Waiting is an explicit synchronization
	// point chosen by the model when it is ready to synthesize child results.
	wait := false
	if args.Wait != nil {
		wait = *args.Wait
	}
	controls, missing := t.Service.selectSubagents(taskIDs, len(taskIDs) == 0)
	if len(controls) == 0 {
		message := "当前没有尚未收集的子代理。"
		if len(missing) > 0 {
			message = "未找到子代理任务: " + strings.Join(missing, ", ")
		}
		return delegatedTaskError(message), nil
	}

	results := make([]delegatedTaskResult, 0, len(controls))
	parts := make([]tools.ResultPart, 0, len(controls)*2)
	running := 0
	if wait {
		tools.EmitProgress(ctx, tools.ResultPart{
			Kind:   tools.PartToolCall,
			Name:   "await_subagents",
			Status: "waiting",
			Output: fmt.Sprintf("正在等待 %d 个后台子代理完成", len(controls)),
		})
	}
	for _, control := range controls {
		if wait {
			select {
			case <-control.done:
			case <-ctx.Done():
			}
		}
		latest, _, finished, _ := control.snapshot()
		if finished {
			result, _, newlyCollected := control.collect()
			results = append(results, result)
			parts = append(parts, result.part)
			if newlyCollected {
				_ = t.Service.persistSubagentControl(control)
				parts = append(parts, result.artifacts...)
				t.Service.recordDelegatedTaskUsage(result)
			}
			continue
		}
		running++
		results = append(results, delegatedTaskResult{part: latest})
		parts = append(parts, latest)
	}

	summary := delegatedTaskSummary(results)
	if running > 0 {
		summary = fmt.Sprintf("仍有 %d 个子代理运行中。\n\n%s", running, summary)
	}
	if len(missing) > 0 {
		summary += "\n\n未找到任务: " + strings.Join(missing, ", ")
	}
	allFinishedWithoutSuccess := running == 0
	if allFinishedWithoutSuccess {
		for _, result := range results {
			if result.part.Status == "completed" {
				allFinishedWithoutSuccess = false
				break
			}
		}
	}
	return tools.Result{
		Summary: clipContextText(summary, 16_000),
		Parts:   parts,
		IsError: allFinishedWithoutSuccess,
	}, nil
}

func normalizeSubagentTaskIDs(input []string) []string {
	result := make([]string, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, taskID := range input {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" || seen[taskID] {
			continue
		}
		seen[taskID] = true
		result = append(result, taskID)
	}
	return result
}

func (s *Service) selectSubagents(taskIDs []string, uncollectedOnly bool) ([]*subagentControl, []string) {
	s.subagentMu.Lock()
	controlsByID := make(map[string]*subagentControl, len(s.subagents))
	for taskID, control := range s.subagents {
		controlsByID[taskID] = control
	}
	s.subagentMu.Unlock()

	missing := make([]string, 0)
	controls := make([]*subagentControl, 0, len(controlsByID))
	if len(taskIDs) > 0 {
		for _, taskID := range taskIDs {
			control := controlsByID[taskID]
			if control == nil {
				missing = append(missing, taskID)
				continue
			}
			controls = append(controls, control)
		}
		return controls, missing
	}

	for _, control := range controlsByID {
		if uncollectedOnly {
			_, collected := control.stateFlags()
			if collected {
				continue
			}
		}
		controls = append(controls, control)
	}
	sort.Slice(controls, func(left, right int) bool {
		return controls[left].taskID < controls[right].taskID
	})
	return controls, nil
}

func (s *Service) activeSubagentCount() int {
	controls, _ := s.selectSubagents(nil, false)
	active := 0
	for _, control := range controls {
		finished, _ := control.stateFlags()
		if !finished {
			active++
		}
	}
	return active
}

func (s *Service) hasUncollectedSubagents() bool {
	controls, _ := s.selectSubagents(nil, true)
	return len(controls) > 0
}

// finishSubagentTurn is retained for callers that do not have a parent task
// context. Production chat turns use finishSubagentTurnWithContext so a task
// cancellation also stops every worker before the parent can commit or roll
// back its state.
func (s *Service) finishSubagentTurn(cancelWorkers bool) []tools.ResultPart {
	return s.finishSubagentTurnWithContext(context.Background(), cancelWorkers)
}

// finishSubagentTurnWithContext is the final lifecycle barrier. A parent
// cancellation is forwarded to all workers before joining them, so a stalled
// child cannot leave the parent waiting while its task watchdog has already
// decided that the turn must end.
func (s *Service) finishSubagentTurnWithContext(ctx context.Context, cancelWorkers bool) []tools.ResultPart {
	if ctx == nil {
		ctx = context.Background()
	}
	controls, _ := s.selectSubagents(nil, false)
	cancelAll := func() {
		for _, control := range controls {
			if control.cancel != nil {
				control.cancel()
			}
		}
	}
	workersCancelled := cancelWorkers
	if workersCancelled {
		cancelAll()
	}
	var cancellationDeadline time.Time
	startCancellationDeadline := func() {
		if !cancellationDeadline.IsZero() {
			return
		}
		cancellationDeadline = time.Now().Add(subagentCancellationJoinGrace)
	}
	waitForCancellation := func(control *subagentControl) bool {
		remaining := time.Until(cancellationDeadline)
		if remaining <= 0 {
			return false
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-control.done:
			return true
		case <-timer.C:
			return false
		}
	}
	if workersCancelled {
		startCancellationDeadline()
	}
	detached := make(map[string]tools.ResultPart)
	for _, control := range controls {
		if workersCancelled {
			if !waitForCancellation(control) {
				detached[control.taskID] = detachedSubagentPart(control)
			}
			continue
		}
		select {
		case <-control.done:
		case <-ctx.Done():
			cancelAll()
			workersCancelled = true
			startCancellationDeadline()
			if !waitForCancellation(control) {
				detached[control.taskID] = detachedSubagentPart(control)
			}
		}
	}

	parts := make([]tools.ResultPart, 0, len(controls)*2)
	for _, control := range controls {
		if part, detached := detached[control.taskID]; detached {
			if control.finish(delegatedTaskResult{part: part}) {
				_ = s.persistSubagentControl(control)
			}
			parts = append(parts, part)
			continue
		}
		result, finished, newlyCollected := control.collect()
		if !finished {
			continue
		}
		parts = append(parts, result.part)
		if newlyCollected {
			_ = s.persistSubagentControl(control)
			parts = append(parts, result.artifacts...)
			s.recordDelegatedTaskUsage(result)
		}
	}

	s.subagentMu.Lock()
	for _, control := range controls {
		if s.subagents[control.taskID] == control {
			delete(s.subagents, control.taskID)
		}
	}
	s.subagentMu.Unlock()
	return parts
}

func detachedSubagentPart(control *subagentControl) tools.ResultPart {
	part, _, _, _ := control.snapshot()
	part.Status = "cancelled"
	part.Summary = "子代理未在取消宽限期内停止，已从当前任务分离并转入后台清理。"
	part.CurrentAction = "已停止并分离"
	part.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if part.DurationMs < 1 {
		part.DurationMs = 1
	}
	for index := range part.Steps {
		if part.Steps[index].Status == "pending" || part.Steps[index].Status == "in_progress" {
			part.Steps[index].Status = "cancelled"
		}
	}
	return part
}

func (s *Service) recordDelegatedTaskUsage(result delegatedTaskResult) {
	if len(result.usage) == 0 || result.route.Provider.ID == "" {
		return
	}
	for index := range result.usage {
		usage := result.usage[index]
		s.recordUsageMetrics(usageMetricsFor(result.route.Provider, result.route.ModelID, &usage), result.route)
	}
}

func normalizeDelegatedTaskSpecs(input []delegateTaskSpec, limit int) ([]delegateTaskSpec, error) {
	limit = normalizeSubagentConcurrencyLimit(limit)
	if len(input) == 0 {
		return nil, fmt.Errorf("至少需要一个子任务")
	}
	if len(input) > limit {
		return nil, fmt.Errorf("一次最多委派 %d 个子任务", limit)
	}
	output := make([]delegateTaskSpec, len(input))
	for index, spec := range input {
		spec.Label = strings.TrimSpace(spec.Label)
		spec.Task = strings.TrimSpace(spec.Task)
		spec.AgentType = strings.ToLower(strings.TrimSpace(spec.AgentType))
		spec.ProviderID = strings.TrimSpace(spec.ProviderID)
		spec.Model = strings.TrimSpace(spec.Model)
		if spec.Label == "" {
			return nil, fmt.Errorf("第 %d 个子任务缺少 label", index+1)
		}
		if spec.Task == "" {
			return nil, fmt.Errorf("第 %d 个子任务缺少 task", index+1)
		}
		switch spec.AgentType {
		case subagentExplore, subagentReview, subagentImplement:
		default:
			return nil, fmt.Errorf("第 %d 个子任务的 agentType 无效: %s", index+1, spec.AgentType)
		}
		spec.Label = clipContextText(spec.Label, 80)
		spec.Task = clipContextText(spec.Task, 8_000)
		output[index] = spec
	}
	return output, nil
}

func (s *Service) runDelegatedTask(
	ctx context.Context,
	scope subagentExecutionScope,
	spec delegateTaskSpec,
	part tools.ResultPart,
	emit func(tools.ResultPart),
) delegatedTaskResult {
	startedAt := time.Now()
	part.StartedAt = startedAt.Format(time.RFC3339Nano)
	part.Status = "running"
	part.Summary = ""
	part.CurrentAction = "正在准备独立上下文"
	part.Steps = []tools.ProgressStep{{Title: "准备独立上下文", Status: "in_progress"}}
	emit(part)

	finish := func(status, summary, action string, outcome toolLoopOutcome, route chatRoute) delegatedTaskResult {
		completedAt := time.Now()
		part.Status = status
		part.Summary = clipContextText(strings.TrimSpace(summary), 8_000)
		part.CurrentAction = action
		if content := sanitizeModelContent(outcome.Content); content != "" {
			part.SubagentOutput = boundedSubagentText("", content)
		}
		if reasoning := strings.TrimSpace(outcome.Reasoning); reasoning != "" {
			part.SubagentReasoning = boundedSubagentText("", reasoning)
		}
		if part.SubagentOutput == "" && strings.TrimSpace(summary) != "" {
			part.SubagentOutput = boundedSubagentText("", summary)
		}
		part.CompletedAt = completedAt.Format(time.RFC3339Nano)
		part.DurationMs = completedAt.Sub(startedAt).Milliseconds()
		if part.DurationMs < 1 {
			part.DurationMs = 1
		}
		for index := range part.Steps {
			if part.Steps[index].Status == "in_progress" {
				if status == "completed" {
					part.Steps[index].Status = "completed"
				} else {
					part.Steps[index].Status = status
				}
			}
		}
		files, additions, deletions := tools.SummarizeFileChanges(outcome.Changes)
		part.ChangedFiles = files
		part.Additions = additions
		part.Deletions = deletions
		emit(part)
		usageSamples := append([]protocol.TokenUsage(nil), outcome.UsageSamples...)
		if len(usageSamples) == 0 && outcome.Usage != nil {
			usageSamples = append(usageSamples, *outcome.Usage)
		}
		return delegatedTaskResult{
			part:      part,
			artifacts: delegatedTaskArtifacts(outcome.Parts),
			usage:     usageSamples,
			route:     route,
		}
	}

	if err := ctx.Err(); err != nil {
		return finish("cancelled", "任务在开始前已停止", "已停止", toolLoopOutcome{}, chatRoute{})
	}
	route, err := s.resolveSubagentRoute(spec, scope.PrimaryRoute)
	if err != nil {
		return finish("error", err.Error(), "路由失败", toolLoopOutcome{}, chatRoute{})
	}
	part.ProviderID = route.Provider.ID
	part.Model = route.ModelID
	provider, err := s.chatProviderForRoute(route)
	if err != nil {
		return finish("error", err.Error(), "模型连接失败", toolLoopOutcome{}, route)
	}

	part.Steps[0].Status = "completed"
	part.Steps = append(part.Steps, tools.ProgressStep{Title: "分析任务", Status: "in_progress"})
	part.CurrentAction = "正在分析任务"
	emit(part)

	request := subagentRequest(scope.BaseRequest, spec, part.TaskID, route)
	registry := s.buildReadOnlyRegistryForContext(ctx)
	if spec.AgentType == subagentImplement {
		registry = s.buildWorkerToolRegistryForContext(ctx)
	}
	stepIndex := make(map[string]int)
	activityIndex := make(map[string]int)
	lastStreamEmit := time.Time{}
	emitStream := func(force bool) {
		if !force && time.Since(lastStreamEmit) < 75*time.Millisecond {
			return
		}
		lastStreamEmit = time.Now()
		emit(part)
	}
	childSink := func(event ChatStreamEvent) {
		switch event.Type {
		case "delta":
			part.SubagentOutput = boundedSubagentText(part.SubagentOutput, event.Delta)
			emitStream(false)
		case "reasoning":
			part.SubagentReasoning = boundedSubagentText(part.SubagentReasoning, event.Delta)
			emitStream(false)
		case "tool":
			key := strings.TrimSpace(event.ToolCallID)
			if key == "" {
				key = event.ToolName + "\x00" + event.ToolInput
			}
			index, exists := stepIndex[key]
			if !exists && len(part.Steps) < maxSubagentTimelineSteps-1 {
				title := subagentToolStepTitle(event.ToolName, event.ToolInput)
				part.Steps = append(part.Steps, tools.ProgressStep{Title: title, Status: "in_progress"})
				index = len(part.Steps) - 1
				stepIndex[key] = index
				exists = true
			} else if !exists {
				markSubagentStepOverflow(&part)
			}
			if exists {
				switch event.Status {
				case "error":
					part.Steps[index].Status = "error"
				case "completed", "ok":
					part.Steps[index].Status = "completed"
				default:
					part.Steps[index].Status = "in_progress"
				}
			}
			if message := strings.TrimSpace(event.Message); message != "" {
				part.CurrentAction = clipContextText(message, 240)
			} else {
				part.CurrentAction = subagentToolStepTitle(event.ToolName, event.ToolInput)
			}
			upsertSubagentActivity(&part, activityIndex, event)
			emit(part)
		case "status", "context_compression":
			if message := strings.TrimSpace(event.Message); message != "" {
				part.CurrentAction = clipContextText(message, 240)
				emit(part)
			}
		case "provider_notice":
			appendSubagentProviderActivity(&part, event)
			emit(part)
		}
	}

	outcome, runErr := s.runStreamingToolLoop(ctx, provider, registry, request, childSink)
	emitStream(true)
	content := sanitizeModelContent(outcome.Content)
	if content == "" {
		content = partialToolFailureContent(outcome)
	}
	if ctx.Err() != nil {
		if content == "" {
			content = "子任务已停止"
		}
		return finish("cancelled", content, "已停止", outcome, route)
	}
	if runErr != nil {
		if content == "" {
			content = redactSensitiveText(runErr.Error())
		}
		return finish("error", content, "执行失败", outcome, route)
	}
	if content == "" {
		content = "子任务已完成，未返回额外说明。"
	}
	return finish("completed", content, "已完成", outcome, route)
}

func (s *Service) resolveSubagentRoute(spec delegateTaskSpec, primary chatRoute) (chatRoute, error) {
	if spec.ProviderID == "" {
		route := primary
		if spec.Model != "" {
			route.ModelID = spec.Model
		}
		return route, nil
	}
	runtime := s.stateRuntimeSettings()
	provider, _, ok := findModelProvider(runtime.Model.Providers, spec.ProviderID)
	if !ok || !provider.Enabled {
		return chatRoute{}, fmt.Errorf("子代理供应商不可用: %s", spec.ProviderID)
	}
	route, err := s.chatRouteForProvider(runtime, provider)
	if err != nil {
		return chatRoute{}, err
	}
	if spec.Model != "" {
		route.ModelID = spec.Model
	}
	return route, nil
}

func subagentRequest(base protocol.ChatRequest, spec delegateTaskSpec, taskID string, route chatRoute) protocol.ChatRequest {
	request := base
	applyRouteToChatRequest(&request, route)
	request.Metadata = make(map[string]string, len(base.Metadata)+4)
	for key, value := range base.Metadata {
		request.Metadata[key] = value
	}
	request.Metadata["request_kind"] = "subagent"
	request.Metadata["subagent_task_id"] = taskID
	request.Metadata["subagent_type"] = spec.AgentType
	request.Metadata["parent_turn_id"] = base.TurnID
	if value := strings.TrimSuffix(strings.TrimSpace(base.SessionID), ":"); value != "" {
		request.SessionID = value + ":subagent:" + taskID
	}
	if value := strings.TrimSuffix(strings.TrimSpace(base.ThreadID), ":"); value != "" {
		request.ThreadID = value + ":subagent:" + taskID
	}
	if value := strings.TrimSuffix(strings.TrimSpace(base.TurnID), ":"); value != "" {
		request.TurnID = value + ":subagent:" + taskID
	}
	request.ResponsesContext.RequestKind = "subagent"
	request.ResponsesContext.ThreadSource = "agent"
	if request.ResponsesContext.WindowID != "" {
		request.ResponsesContext.WindowID += ":subagent:" + taskID
	}
	request.Messages = buildSubagentContextMessages(base.Messages, spec)
	request.Tools = nil
	request.ToolChoice = "auto"
	request.ParallelToolCalls = true
	return request
}

func subagentInstruction(spec delegateTaskSpec) string {
	modeRule := "只读探索真实工作区并返回有文件路径和行号的证据；禁止修改文件或运行 Shell。"
	switch spec.AgentType {
	case subagentReview:
		modeRule = "只读审阅真实工作区，优先报告正确性、回归、安全边界与测试缺口；禁止修改文件或运行 Shell。"
	case subagentImplement:
		modeRule = "使用结构化工具完成真实修改和必要验证；只修改子任务明确分配的文件范围，避免触碰主 Agent 或兄弟子代理负责的文件；所有写入、命令、网络与审批继续遵守主 Agent 的沙箱策略。"
	}
	return strings.Join([]string{
		"你是由 EchoCode 主 Agent 动态创建的独立子代理。",
		"不要尝试创建或委派新的子代理；你的工具集中也不会提供 delegate_task。",
		"主 Agent 传入的 task_scope 是本轮授权边界；目标不存在表示待创建。不得读取、搜索或修改范围外的兄弟项目，也不得用其他项目替代目标。",
		modeRule,
		"只处理下面的独立目标，不要重新执行整个用户任务。完成后给主 Agent 一份简洁、可核验的结果摘要。",
		"",
		"子任务：" + spec.Label,
		spec.Task,
	}, "\n")
}

func subagentToolStepTitle(name, input string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "工具"
	}
	switch label {
	case "read_file":
		label = "读取文件"
	case "list_dir":
		label = "查看目录"
	case "search":
		label = "搜索代码"
	case "read_repository":
		label = "读取仓库"
	case "read_webpage":
		label = "读取网页"
	case "web_search":
		label = "搜索网络"
	case "write_file":
		label = "写入文件"
	case "apply_patch":
		label = "修改文件"
	case "download_file":
		label = "下载文件"
	case "run_command":
		label = "运行命令"
	case "git":
		label = "检查 Git"
	case "git_repository":
		label = "拉取仓库"
	case "terminal":
		label = "操作终端"
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return label
	}
	return clipContextText(label+" · "+input, 180)
}

func delegatedTaskArtifacts(parts []tools.ResultPart) []tools.ResultPart {
	artifacts := make([]tools.ResultPart, 0)
	seen := make(map[string]bool)
	for _, part := range parts {
		if part.Kind != tools.PartDiff && part.Kind != tools.PartFile {
			continue
		}
		key := string(part.Kind) + "\x00" + part.Path + "\x00" + part.Patch
		if seen[key] {
			continue
		}
		seen[key] = true
		artifacts = append(artifacts, part)
	}
	return artifacts
}

func delegatedTaskStartSummary(parts []tools.ResultPart) string {
	var out strings.Builder
	fmt.Fprintf(&out, "已在后台启动 %d 个子代理。主 Agent 应继续处理可独立推进且文件范围不重叠的工作，并在最终综合前调用 await_subagents 收集结果。", len(parts))
	for _, part := range parts {
		label := strings.TrimSpace(part.Label)
		if label == "" {
			label = "子任务"
		}
		fmt.Fprintf(&out, "\n- %s: %s (%s)", part.TaskID, label, part.AgentType)
	}
	return clipContextText(out.String(), 4_000)
}

func delegatedTaskSummary(results []delegatedTaskResult) string {
	var out strings.Builder
	completed := 0
	for _, result := range results {
		if result.part.Status == "completed" {
			completed++
		}
	}
	fmt.Fprintf(&out, "子代理完成 %d/%d 个任务。", completed, len(results))
	for _, result := range results {
		label := strings.TrimSpace(result.part.Label)
		if label == "" {
			label = result.part.TaskID
		}
		fmt.Fprintf(&out, "\n\n[%s · %s · %s]", label, result.part.AgentType, result.part.Status)
		if summary := strings.TrimSpace(result.part.Summary); summary != "" {
			out.WriteString("\n")
			out.WriteString(clipContextText(summary, 6_000))
		}
	}
	return clipContextText(out.String(), 16_000)
}

func delegatedTaskError(message string) tools.Result {
	return tools.Result{Summary: message, IsError: true}
}

func cloneSubagentPart(part tools.ResultPart) tools.ResultPart {
	part.Steps = append([]tools.ProgressStep(nil), part.Steps...)
	part.Activities = append([]tools.SubagentActivity(nil), part.Activities...)
	return part
}

func boundedSubagentText(current, addition string) string {
	if addition == "" {
		return current
	}
	runes := []rune(current + addition)
	if len(runes) <= maxSubagentOutputRunes {
		return string(runes)
	}
	return "[较早的输出已省略]\n" + string(runes[len(runes)-maxSubagentOutputRunes:])
}

func normalizeSubagentConcurrencyLimit(value int) int {
	if value <= 0 {
		return defaultMaxConcurrentSubagents
	}
	if value > maxConcurrentSubagents {
		return maxConcurrentSubagents
	}
	return value
}

func (s *Service) subagentConcurrencyLimit() int {
	if s == nil {
		return defaultMaxConcurrentSubagents
	}
	return normalizeSubagentConcurrencyLimit(s.runtimeSettings.MaxConcurrentSubagents)
}

func markSubagentStepOverflow(part *tools.ResultPart) {
	if part == nil {
		return
	}
	for _, step := range part.Steps {
		if step.Title == subagentStepOverflow {
			return
		}
	}
	marker := tools.ProgressStep{Title: subagentStepOverflow, Status: "in_progress"}
	if len(part.Steps) >= maxSubagentTimelineSteps {
		part.Steps[len(part.Steps)-1] = marker
		return
	}
	part.Steps = append(part.Steps, marker)
}

func markSubagentActivityOverflow(part *tools.ResultPart) {
	if part == nil {
		return
	}
	for _, activity := range part.Activities {
		if activity.ID == subagentActivityOverflowID {
			return
		}
	}
	marker := tools.SubagentActivity{
		ID: subagentActivityOverflowID, Kind: "status", Title: "活动记录已折叠",
		Status: "running", Output: subagentActivityOverflow, StartedAt: time.Now().Format(time.RFC3339Nano),
	}
	if len(part.Activities) >= maxSubagentActivities {
		part.Activities[len(part.Activities)-1] = marker
		return
	}
	part.Activities = append(part.Activities, marker)
}

func upsertSubagentActivity(part *tools.ResultPart, indexes map[string]int, event ChatStreamEvent) {
	if part == nil {
		return
	}
	key := strings.TrimSpace(event.ToolCallID)
	if key == "" {
		key = event.ToolName + "\x00" + event.ToolInput
	}
	index, exists := indexes[key]
	if !exists {
		if len(part.Activities) >= maxSubagentActivities-1 {
			markSubagentActivityOverflow(part)
			return
		}
		index = len(part.Activities)
		indexes[key] = index
		part.Activities = append(part.Activities, tools.SubagentActivity{
			ID: key, Kind: "tool", Title: subagentToolStepTitle(event.ToolName, event.ToolInput),
			Status: "running", Input: strings.TrimSpace(event.ToolInput), StartedAt: time.Now().Format(time.RFC3339Nano),
		})
	}
	activity := &part.Activities[index]
	if event.Status != "" {
		activity.Status = event.Status
	}
	if output := subagentActivityOutput(event); output != "" {
		activity.Output = boundedSubagentText(activity.Output, output)
	}
	if event.Status == "completed" || event.Status == "ok" || event.Status == "error" {
		completed := time.Now()
		activity.CompletedAt = completed.Format(time.RFC3339Nano)
		if started, err := time.Parse(time.RFC3339Nano, activity.StartedAt); err == nil {
			activity.DurationMs = completed.Sub(started).Milliseconds()
			if activity.DurationMs < 1 {
				activity.DurationMs = 1
			}
		}
	}
}

func subagentActivityOutput(event ChatStreamEvent) string {
	var output strings.Builder
	for _, part := range event.Parts {
		for _, value := range []string{part.Output, part.Stdout, part.Stderr} {
			value = strings.TrimSpace(value)
			if value == "" || strings.Contains(output.String(), value) {
				continue
			}
			if output.Len() > 0 {
				output.WriteString("\n")
			}
			output.WriteString(value)
		}
	}
	if output.Len() == 0 && event.Status != "running" {
		output.WriteString(strings.TrimSpace(event.Message))
	}
	return output.String()
}

func appendSubagentProviderActivity(part *tools.ResultPart, event ChatStreamEvent) {
	if part == nil {
		return
	}
	if len(part.Activities) >= maxSubagentActivities-1 {
		markSubagentActivityOverflow(part)
		return
	}
	message := strings.TrimSpace(event.Message)
	if message == "" {
		return
	}
	part.Activities = append(part.Activities, tools.SubagentActivity{
		ID: fmt.Sprintf("provider-%d", time.Now().UnixNano()), Kind: "provider", Title: "模型运行信息",
		Status: "completed", Output: message, StartedAt: time.Now().Format(time.RFC3339Nano),
	})
}
