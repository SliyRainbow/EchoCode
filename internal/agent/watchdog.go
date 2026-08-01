package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

var (
	ErrTaskIdleTimeout      = errors.New("agent task idle timeout")
	ErrToolExecutionTimeout = errors.New("tool execution timeout")
	nilServiceMutationGate  sync.Mutex
)

type taskWatchdogControl struct {
	stopOnce sync.Once
	stop     chan struct{}
	cancel   context.CancelCauseFunc
}

type taskActivityReporter struct {
	touch    func()
	interval time.Duration
}

type taskActivityReporterKey struct{}

const (
	minTaskToolHeartbeatInterval = 10 * time.Millisecond
	maxTaskToolHeartbeatInterval = 15 * time.Second
)

func (control *taskWatchdogControl) pause() {
	if control == nil {
		return
	}
	control.stopOnce.Do(func() { close(control.stop) })
}

func (control *taskWatchdogControl) close() {
	if control == nil {
		return
	}
	control.pause()
	control.cancel(context.Canceled)
}

func withTaskIdleWatchdog(
	parent context.Context,
	timeout time.Duration,
	sink ChatEventSink,
) (context.Context, ChatEventSink, *taskWatchdogControl) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return parent, sink, nil
	}

	ctx, cancel := context.WithCancelCause(parent)
	activity := make(chan struct{}, 1)
	control := &taskWatchdogControl{stop: make(chan struct{}), cancel: cancel}
	serializedSink := serializedChatEventSink(sink)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	wrappedSink := func(event ChatStreamEvent) {
		touch()
		emitChatEvent(serializedSink, event)
	}
	ctx = context.WithValue(ctx, taskActivityReporterKey{}, taskActivityReporter{
		touch:    touch,
		interval: taskToolHeartbeatInterval(timeout),
	})

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-control.stop:
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-timer.C:
				cause := fmt.Errorf("%w after %s without model or tool activity", ErrTaskIdleTimeout, timeout)
				emitChatEvent(serializedSink, ChatStreamEvent{
					Type:    "status",
					Status:  "failed",
					Message: fmt.Sprintf("任务已连续 %s 没有模型或工具活动，EchoCode 已结束本轮任务。", timeout),
				})
				cancel(cause)
				return
			}
		}
	}()
	touch()
	return ctx, wrappedSink, control
}

func taskToolHeartbeatInterval(idleTimeout time.Duration) time.Duration {
	if idleTimeout <= 0 {
		return 0
	}
	interval := idleTimeout / 3
	if interval < minTaskToolHeartbeatInterval {
		return minTaskToolHeartbeatInterval
	}
	if interval > maxTaskToolHeartbeatInterval {
		return maxTaskToolHeartbeatInterval
	}
	return interval
}

// startTaskToolHeartbeat distinguishes an active but quiet tool from a stalled
// turn. It is intentionally not a UI event: real tool output remains the only
// user-visible progress, while the tool watchdog still provides a hard bound.
func startTaskToolHeartbeat(ctx context.Context) func() {
	if ctx == nil {
		return func() {}
	}
	reporter, ok := ctx.Value(taskActivityReporterKey{}).(taskActivityReporter)
	if !ok || reporter.touch == nil || reporter.interval <= 0 {
		return func() {}
	}
	reporter.touch()
	stop := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		ticker := time.NewTicker(reporter.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				reporter.touch()
			}
		}
	}()
	return func() {
		stopOnce.Do(func() { close(stop) })
	}
}

func resolvedTaskContextError(ctx context.Context, err error) error {
	if ctx == nil {
		return err
	}
	cause := context.Cause(ctx)
	if !errors.Is(cause, ErrTaskIdleTimeout) {
		return err
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cause
	}
	return err
}

func chatTurnWasCancelled(ctx context.Context, err error) bool {
	if ctx != nil && errors.Is(context.Cause(ctx), ErrTaskIdleTimeout) {
		return false
	}
	return errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))
}

type toolExecutionOutcome struct {
	result tools.Result
	err    error
}

func (s *Service) runToolWithWatchdog(
	ctx context.Context,
	tool tools.Tool,
	name string,
	rawArgs json.RawMessage,
	timeout time.Duration,
) (tools.Result, error) {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	retainsTaskContext := false
	if backgroundTool, ok := tool.(interface{ RetainsTaskContext() bool }); ok {
		retainsTaskContext = backgroundTool.RetainsTaskContext()
	}
	if err := ctx.Err(); err != nil {
		if retainsTaskContext {
			// Retained-context tools still need one synchronous cleanup pass after
			// cancellation. DelegateTaskTool uses this path to persist a cancelled
			// child entry instead of losing the subagent card entirely.
			return s.runToolWithApproval(ctx, tool, name, rawArgs)
		}
		return tools.Result{}, err
	}
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executionCtx := toolCtx
	if retainsTaskContext {
		executionCtx = ctx
	}
	stopHeartbeat := startTaskToolHeartbeat(executionCtx)
	defer stopHeartbeat()
	resourcePlan := s.resourcesForTool(name, tool, rawArgs)

	done := make(chan toolExecutionOutcome, 1)
	go func() {
		if len(resourcePlan.Requests) > 0 {
			lease, err := s.resourceCoordinatorForWorkspace().AcquireAll(toolCtx, resourcePlan.Requests)
			if err != nil {
				select {
				case done <- toolExecutionOutcome{err: err}:
				case <-toolCtx.Done():
				}
				return
			}
			defer lease.Release()
		}
		if toolNeedsExclusiveWorkspaceAccess(name, tool) && !resourcePlan.Precise {
			unlock, acquired := s.lockToolMutationUntilDone(toolCtx)
			if !acquired {
				return
			}
			defer unlock()
		}
		if err := executionCtx.Err(); err != nil {
			return
		}
		result, err := s.runToolWithApproval(executionCtx, tool, name, rawArgs)
		select {
		case done <- toolExecutionOutcome{result: result, err: err}:
		case <-toolCtx.Done():
		}
	}()

	select {
	case outcome := <-done:
		return outcome.result, outcome.err
	case <-toolCtx.Done():
		if cause := context.Cause(ctx); cause != nil {
			return tools.Result{}, cause
		}
		if !errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
			return tools.Result{}, toolCtx.Err()
		}
		summary := fmt.Sprintf("工具 %s 执行超过 %s，EchoCode 已取消当前工具并忽略迟到结果。", name, timeout)
		return tools.Result{
			Summary: summary,
			IsError: true,
			Parts: []tools.ResultPart{{
				Kind:   tools.PartToolCall,
				Name:   name,
				Status: "error",
				Output: summary,
			}},
		}, nil
	}
}

// lockToolMutationUntilDone waits for the exclusive workspace gate without
// trapping a cancelled task behind another mutating tool. sync.Mutex does not
// support context-aware waiting, so use short TryLock intervals and abandon
// the wait as soon as the tool deadline or parent task is cancelled.
func (s *Service) lockToolMutationUntilDone(ctx context.Context) (func(), bool) {
	if s == nil {
		return func() {}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	gate := s.toolMutationGateForWorkspace()
	if gate.TryLock() {
		return gate.Unlock, true
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return func() {}, false
		case <-ticker.C:
			if gate.TryLock() {
				return gate.Unlock, true
			}
		}
	}
}

func (s *Service) toolMutationGateForWorkspace() *sync.Mutex {
	if s == nil {
		return &nilServiceMutationGate
	}
	if s.toolMutationGates == nil {
		return &s.toolMutationMu
	}
	root := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot)
	if root == "" {
		return &s.toolMutationMu
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	gate, _ := s.toolMutationGates.LoadOrStore(root, &sync.Mutex{})
	return gate.(*sync.Mutex)
}
