package agent

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestTaskRuntimeGenerationRejectsLateStreamAndTerminalWrites(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	oldGeneration, err := service.StartTaskRuntimeWithGeneration("task-reused", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration, err := service.StartTaskRuntimeWithGeneration("task-reused", "2026-07-30T01:00:01Z")
	if err != nil {
		t.Fatal(err)
	}
	if oldGeneration == 0 || currentGeneration <= oldGeneration {
		t.Fatalf("generations = old:%d current:%d", oldGeneration, currentGeneration)
	}

	accepted, err := service.RecordTaskStreamEventForGeneration("task-reused", oldGeneration, ChatStreamEvent{
		Type: "delta", Delta: "stale output",
	})
	if err != nil || accepted {
		t.Fatalf("stale stream accepted=%v err=%v", accepted, err)
	}
	accepted, err = service.FinishTaskRuntimeForGeneration(
		"task-reused", oldGeneration, "completed", "stale completion", ChatResult{Content: "stale result"},
	)
	if err != nil || accepted {
		t.Fatalf("stale terminal accepted=%v err=%v", accepted, err)
	}

	accepted, err = service.RecordTaskStreamEventForGeneration("task-reused", currentGeneration, ChatStreamEvent{
		Type: "delta", Delta: "current output",
	})
	if err != nil || !accepted {
		t.Fatalf("current stream accepted=%v err=%v", accepted, err)
	}
	accepted, err = service.FinishTaskRuntimeForGeneration(
		"task-reused", currentGeneration, "completed", "current completion", ChatResult{},
	)
	if err != nil || !accepted {
		t.Fatalf("current terminal accepted=%v err=%v", accepted, err)
	}

	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Generation != currentGeneration || snapshot.Status != "completed" || snapshot.Content != "current output" {
		t.Fatalf("current snapshot = %#v ok=%v", snapshot, ok)
	}
}

func TestTaskRuntimeGenerationRejectsConcurrentStaleWriters(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	oldGeneration, err := service.StartTaskRuntimeWithGeneration("task-race", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration, err := service.StartTaskRuntimeWithGeneration("task-race", "2026-07-30T01:00:01Z")
	if err != nil {
		t.Fatal(err)
	}

	var acceptedWrites atomic.Int64
	errors := make(chan error, 128)
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			accepted, recordErr := service.RecordTaskStreamEventForGeneration("task-race", oldGeneration, ChatStreamEvent{
				Type: "delta", Delta: "late",
			})
			if recordErr != nil {
				errors <- recordErr
			}
			if accepted {
				acceptedWrites.Add(1)
			}
		}()
		go func() {
			defer wait.Done()
			accepted, finishErr := service.FinishTaskRuntimeForGeneration(
				"task-race", oldGeneration, "failed", "late failure", ChatResult{Content: "late"},
			)
			if finishErr != nil {
				errors <- finishErr
			}
			if accepted {
				acceptedWrites.Add(1)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for writeErr := range errors {
		t.Fatal(writeErr)
	}
	if acceptedWrites.Load() != 0 {
		t.Fatalf("accepted %d stale writes", acceptedWrites.Load())
	}

	accepted, err := service.FinishTaskRuntimeForGeneration(
		"task-race", currentGeneration, "completed", "done", ChatResult{Content: "current"},
	)
	if err != nil || !accepted {
		t.Fatalf("current terminal accepted=%v err=%v", accepted, err)
	}
	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Generation != currentGeneration || snapshot.Status != "completed" || snapshot.Content != "current" {
		t.Fatalf("snapshot after stale race = %#v ok=%v", snapshot, ok)
	}
}

func TestTaskRuntimeCancellingAndInterruptedSnapshots(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	generation, err := service.StartTaskRuntimeWithGeneration("task-cancel", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := service.RecordTaskStreamEventForGeneration("task-cancel", generation, ChatStreamEvent{
		Type: "tool", ToolName: "run_command", ToolCallID: "command-1", Status: "running",
	})
	if err != nil || !accepted {
		t.Fatalf("tool start accepted=%v err=%v", accepted, err)
	}

	accepted, err = service.MarkTaskRuntimeCancelling("task-cancel", generation, "正在停止任务")
	if err != nil || !accepted {
		t.Fatalf("cancelling accepted=%v err=%v", accepted, err)
	}
	snapshot, ok := service.TaskRuntimeSnapshot()
	if !ok || snapshot.Status != "cancelling" || snapshot.Terminal() {
		t.Fatalf("cancelling snapshot = %#v ok=%v", snapshot, ok)
	}
	accepted, err = service.RecordTaskStreamEventForGeneration("task-cancel", generation, ChatStreamEvent{
		Type: "delta", Delta: "late after cancel request",
	})
	if err != nil || accepted {
		t.Fatalf("post-cancel stream accepted=%v err=%v", accepted, err)
	}

	accepted, err = service.MarkTaskRuntimeInterrupted("task-cancel", generation, "任务已中断")
	if err != nil || !accepted {
		t.Fatalf("interrupted accepted=%v err=%v", accepted, err)
	}
	snapshot, ok = service.TaskRuntimeSnapshot()
	if !ok || !snapshot.Terminal() || snapshot.Status != "interrupted" || len(snapshot.Parts) != 1 {
		t.Fatalf("interrupted snapshot = %#v ok=%v", snapshot, ok)
	}
	if snapshot.Parts[0].Kind != tools.PartToolCall || snapshot.Parts[0].Status != "error" || snapshot.Parts[0].Output == "" {
		t.Fatalf("interrupted tool result = %#v", snapshot.Parts[0])
	}
	accepted, err = service.FinishTaskRuntimeForGeneration(
		"task-cancel", generation, "completed", "late completion", ChatResult{Content: "late"},
	)
	if err != nil || accepted {
		t.Fatalf("terminal overwrite accepted=%v err=%v", accepted, err)
	}
}

func TestGuidedTaskRuntimeStartsNewGeneration(t *testing.T) {
	service := newTaskRuntimeTestService(t, t.TempDir())
	first, err := service.StartTaskRuntimeWithGeneration("task-guided", "2026-07-30T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	guided, err := service.StartGuidedTaskRuntimeWithGeneration("task-guided", "继续核验", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if guided <= first {
		t.Fatalf("guided generation = %d, want greater than %d", guided, first)
	}
	accepted, err := service.RecordTaskStreamEventForGeneration("task-guided", first, ChatStreamEvent{Type: "delta", Delta: "late"})
	if err != nil || accepted {
		t.Fatalf("previous turn accepted=%v err=%v", accepted, err)
	}
	accepted, err = service.RecordTaskStreamEventForGeneration("task-guided", guided, ChatStreamEvent{Type: "delta", Delta: "current"})
	if err != nil || !accepted {
		t.Fatalf("guided turn accepted=%v err=%v", accepted, err)
	}
}
