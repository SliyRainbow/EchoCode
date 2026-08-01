package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type PlanState struct {
	Revision  int                  `json:"revision"`
	Status    string               `json:"status"`
	Steps     []tools.ProgressStep `json:"steps"`
	UpdatedAt string               `json:"updatedAt,omitempty"`
}

func clonePlanState(state PlanState) PlanState {
	state.Steps = append([]tools.ProgressStep(nil), state.Steps...)
	return state
}

func planStepsFromText(plan string) []tools.ProgressStep {
	steps := make([]tools.ProgressStep, 0, 8)
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			line = strings.TrimSpace(line[1:])
		} else if dot := strings.IndexByte(line, '.'); dot > 0 {
			isNumber := true
			for _, r := range line[:dot] {
				if r < '0' || r > '9' {
					isNumber = false
					break
				}
			}
			if isNumber {
				line = strings.TrimSpace(line[dot+1:])
			}
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "[ ]"))
		if line == "" {
			continue
		}
		steps = append(steps, tools.ProgressStep{Title: line, Status: "pending"})
		if len(steps) == 12 {
			break
		}
	}
	return steps
}

func (s *Service) updatePlanState(steps []tools.ProgressStep) error {
	return s.setPlanState(steps, true)
}

func (s *Service) startPlanState(steps []tools.ProgressStep) error {
	return s.setPlanState(steps, false)
}

func (s *Service) finishPlanState(status string) error {
	if len(s.planState.Steps) == 0 {
		return nil
	}
	if status != "completed" && status != "cancelled" && status != "failed" {
		return fmt.Errorf("invalid terminal plan status %q", status)
	}
	steps := append([]tools.ProgressStep(nil), s.planState.Steps...)
	if status == "completed" {
		for index := range steps {
			steps[index].Status = "completed"
		}
	}
	return s.persistPlanState(steps, status)
}

func (s *Service) failStartedPlan(started bool, cause error) error {
	if !started {
		return cause
	}
	if err := s.finishPlanState("failed"); err != nil {
		return errors.Join(cause, fmt.Errorf("mark plan failed: %w", err))
	}
	return cause
}

func (s *Service) setPlanState(steps []tools.ProgressStep, rejectCompletedRegression bool) error {
	if rejectCompletedRegression {
		for _, previous := range s.planState.Steps {
			if previous.Status != "completed" {
				continue
			}
			for _, next := range steps {
				if next.Title == previous.Title && next.Status != "completed" {
					return fmt.Errorf("completed plan step %q cannot move backwards", next.Title)
				}
			}
		}
	}
	status := "running"
	completed := 0
	for _, step := range steps {
		if step.Status == "completed" {
			completed++
		}
	}
	if completed == len(steps) {
		status = "completed"
	}
	return s.persistPlanState(steps, status)
}

func (s *Service) persistPlanState(steps []tools.ProgressStep, status string) error {
	if s.planState.Status == status && progressStepsEqual(s.planState.Steps, steps) {
		return nil
	}
	nextState := PlanState{
		Revision:  s.planState.Revision + 1,
		Status:    status,
		Steps:     append([]tools.ProgressStep(nil), steps...),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if s.eventStore != nil {
		eventSteps := make([]eventlog.MessageProgressStep, 0, len(steps))
		for _, step := range steps {
			eventSteps = append(eventSteps, eventlog.MessageProgressStep{Title: step.Title, Status: step.Status})
		}
		if _, err := s.appendEvent(eventlog.EventPayload{PlanSteps: eventSteps, PlanStatus: status}, eventlog.EventPlanUpdate); err != nil {
			return err
		}
	}
	s.planState = nextState
	return nil
}

func progressStepsEqual(left, right []tools.ProgressStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Title != right[index].Title || left[index].Status != right[index].Status {
			return false
		}
	}
	return true
}

// Plan 两段式：high/ultra 档（Planner=true）时，先只读探索产出计划 → 用户批准 → 再执行。
// 复用审批中介：计划作为 kind="plan" 的审批请求弹给用户，批准即进入执行阶段。

const planInstruction = "你现在处于「规划阶段」。请先使用当前可用的只读工具调研真实对象，" +
	"然后用简洁的分步清单给出你打算如何完成任务的计划，不要在本阶段修改任何文件。" +
	"输出格式为 Markdown 有序列表，每步一句话。"

// SetPlanMode 开关 Plan 两段式（用户显式控制）。返回最新工作台状态。
func (s *Service) SetPlanMode(enabled bool) WorkbenchState {
	release, err := s.beginActivity("changing plan mode")
	if err != nil {
		return s.WorkbenchState()
	}
	defer release()
	s.planMode = enabled
	return s.workbenchStateLocked()
}

// PlanMode 返回 Plan 模式是否开启。
func (s *Service) PlanMode() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.planMode
}

// runPlanPhase 执行只读规划阶段，返回计划文本与该阶段产出的片段。
func (s *Service) runPlanPhase(
	ctx context.Context,
	caller protocol.ToolCaller,
	baseRequest protocol.ChatRequest,
	route chatRoute,
	sink ChatEventSink,
) (string, toolLoopOutcome, error) {
	reg := s.buildReadOnlyRegistryForContext(ctx)

	planReq := baseRequest
	planReq.Metadata = make(map[string]string, len(baseRequest.Metadata)+1)
	for key, value := range baseRequest.Metadata {
		planReq.Metadata[key] = value
	}
	planReq.Metadata["request_kind"] = "plan"
	// 在消息尾部追加规划指令（易变尾部，不污染稳定前缀）。
	planReq.Messages = append(append([]protocol.Message{}, baseRequest.Messages...),
		protocol.Message{Role: "user", Content: planInstruction})

	complete := func(ctx context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completion, err := caller.Complete(ctx, request)
		if completion.Usage != nil {
			s.recordLiveUsage(completion.Usage, route, sink)
		}
		return completion, err
	}
	outcome, err := s.runToolLoopWithCompletion(ctx, reg, planReq, complete, nil)
	if err != nil {
		return "", toolLoopOutcome{}, err
	}
	return strings.TrimSpace(outcome.Content), outcome, nil
}

// requestPlanApproval 把计划作为审批请求弹给用户，返回是否批准。
// Plan 审批是 UX 闸门（非安全闸门）：无审批中介或无前端可通知时默认放行执行。
func (s *Service) requestPlanApproval(ctx context.Context, plan string) (bool, error) {
	if s.approvals == nil || !s.approvals.hasNotify() {
		return true, nil
	}
	decision, err := s.approvals.request(ctx, ApprovalRequest{
		Tool:    "plan",
		Kind:    "plan",
		Summary: "AI 提出了执行计划，请审阅后决定是否执行",
		Parts:   []eventlog.MessagePart{{Kind: "text", Text: plan}},
	})
	if err != nil {
		return false, err
	}
	return decision.Approved, nil
}
