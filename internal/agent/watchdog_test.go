package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type contextIgnoringTool struct {
	name    string
	release <-chan struct{}
}

type silentSleepTool struct {
	duration time.Duration
}

type lateMutationProbeTool struct {
	executed chan struct{}
	calls    *atomic.Int32
}

func (lateMutationProbeTool) Name() string { return "mcp__probe__write" }
func (lateMutationProbeTool) Description() string {
	return "records whether a cancelled mutation started"
}
func (lateMutationProbeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (tool lateMutationProbeTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	tool.calls.Add(1)
	close(tool.executed)
	return tools.Result{Summary: "unexpected execution"}, nil
}

func (silentSleepTool) Name() string        { return "silent_sleep" }
func (silentSleepTool) Description() string { return "runs quietly for a bounded duration" }
func (silentSleepTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (tool silentSleepTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	time.Sleep(tool.duration)
	return tools.Result{Summary: "quiet work completed"}, nil
}

func (tool contextIgnoringTool) Name() string {
	if strings.TrimSpace(tool.name) != "" {
		return tool.name
	}
	return "context_ignoring"
}
func (contextIgnoringTool) Description() string { return "waits without honoring context" }
func (contextIgnoringTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func TestToolWatchdogBoundsUncooperativeMCPAndPluginTools(t *testing.T) {
	for _, name := range []string{"mcp__stalled__forever", "plugin__stalled__forever"} {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
			service.runtimeSettings.ApprovalPolicy = "never"
			startedAt := time.Now()
			result, err := service.runToolWithWatchdog(
				context.Background(),
				contextIgnoringTool{name: name, release: release},
				name,
				json.RawMessage(`{}`),
				35*time.Millisecond,
			)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(result.Summary, "忽略迟到结果") {
				t.Fatalf("timeout result = %#v", result)
			}
			if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
				t.Fatalf("watchdog returned too slowly: %s", elapsed)
			}
		})
	}
}
func (tool contextIgnoringTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	<-tool.release
	return tools.Result{Summary: "late result"}, nil
}

func TestToolWatchdogReturnsWhenToolIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	startedAt := time.Now()
	result, err := service.runToolWithWatchdog(
		context.Background(),
		contextIgnoringTool{release: release},
		"context_ignoring",
		json.RawMessage(`{}`),
		35*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Summary, "忽略迟到结果") {
		t.Fatalf("timeout result = %#v", result)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("watchdog returned too slowly: %s", elapsed)
	}
}

func TestToolWatchdogReturnsParentCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Millisecond, cancel)
	startedAt := time.Now()
	_, err := service.runToolWithWatchdog(
		ctx,
		contextIgnoringTool{release: release},
		"context_ignoring",
		json.RawMessage(`{}`),
		time.Second,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation returned too slowly: %s", elapsed)
	}
}

func TestCancelledToolDoesNotStartAfterWaitingForWorkspaceMutationLock(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	gate := service.toolMutationGateForWorkspace()
	gate.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executed := make(chan struct{})
	var calls atomic.Int32
	_, err := service.runToolWithWatchdog(
		ctx,
		lateMutationProbeTool{executed: executed, calls: &calls},
		"mcp__probe__write",
		json.RawMessage(`{}`),
		time.Second,
	)
	if !errors.Is(err, context.Canceled) {
		gate.Unlock()
		t.Fatalf("error = %v, want context cancellation", err)
	}
	gate.Unlock()

	select {
	case <-executed:
		t.Fatal("cancelled tool executed after the workspace lock became available")
	case <-time.After(100 * time.Millisecond):
	}
	if calls.Load() != 0 {
		t.Fatalf("cancelled mutation calls = %d, want 0", calls.Load())
	}
}

func TestTaskIdleWatchdogEmitsFailedTerminalStatus(t *testing.T) {
	var events []ChatStreamEvent
	ctx, _, control := withTaskIdleWatchdog(context.Background(), 35*time.Millisecond, func(event ChatStreamEvent) {
		events = append(events, event)
	})
	defer control.close()
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle watchdog did not cancel the task")
	}
	if !errors.Is(context.Cause(ctx), ErrTaskIdleTimeout) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
	if len(events) != 1 || events[0].Status != "failed" {
		t.Fatalf("events = %#v", events)
	}
}

func TestTaskIdleWatchdogResetsOnActivity(t *testing.T) {
	ctx, sink, control := withTaskIdleWatchdog(context.Background(), 80*time.Millisecond, nil)
	defer control.close()
	for index := 0; index < 4; index++ {
		time.Sleep(30 * time.Millisecond)
		sink(ChatStreamEvent{Type: "status", Status: "running"})
	}
	select {
	case <-ctx.Done():
		t.Fatalf("watchdog expired despite activity: %v", context.Cause(ctx))
	default:
	}
	control.pause()
}

func TestActiveSilentToolKeepsTaskIdleWatchdogAlive(t *testing.T) {
	var events []ChatStreamEvent
	ctx, _, control := withTaskIdleWatchdog(context.Background(), 90*time.Millisecond, func(event ChatStreamEvent) {
		events = append(events, event)
	})
	defer control.close()

	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	result, err := service.runToolWithWatchdog(
		ctx,
		silentSleepTool{duration: 220 * time.Millisecond},
		"silent_sleep",
		json.RawMessage(`{}`),
		time.Second,
	)
	if err != nil || result.IsError {
		t.Fatalf("quiet tool result=%#v err=%v", result, err)
	}
	if errors.Is(context.Cause(ctx), ErrTaskIdleTimeout) {
		t.Fatalf("active quiet tool was treated as an idle task: cause=%v", context.Cause(ctx))
	}
	if len(events) != 0 {
		t.Fatalf("internal heartbeat must not create visible timeline events: %#v", events)
	}
}

func TestResolvedTaskContextErrorKeepsIdleTimeoutAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(fmt.Errorf("%w: test", ErrTaskIdleTimeout))
	err := resolvedTaskContextError(ctx, context.Canceled)
	if !errors.Is(err, ErrTaskIdleTimeout) || chatTurnWasCancelled(ctx, err) {
		t.Fatalf("resolved error = %v, cancelled=%v", err, chatTurnWasCancelled(ctx, err))
	}
}
