package agent

import (
	"strings"
	"sync/atomic"
	"time"
)

// taskRuntimeGenerationClock is process-scoped. A generation only has to
// distinguish live writers: after a process restart no old goroutine remains.
// Durable run identity belongs in the versioned event protocol planned next.
var taskRuntimeGenerationClock atomic.Uint64

func nextTaskRuntimeGeneration() uint64 {
	for {
		if generation := taskRuntimeGenerationClock.Add(1); generation != 0 {
			return generation
		}
	}
}

// StartTaskRuntimeWithGeneration starts a new runtime and returns the token
// that every asynchronous writer should retain. Starting the same task ID
// again invalidates writers from the previous generation.
func (s *Service) StartTaskRuntimeWithGeneration(taskID, startedAt string) (uint64, error) {
	if s == nil || s.eventStore == nil {
		return 0, nil
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()

	startedAt = strings.TrimSpace(startedAt)
	if startedAt == "" {
		startedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	generation := nextTaskRuntimeGeneration()
	s.taskRuntime = TaskRuntimeState{
		TaskID:       strings.TrimSpace(taskID),
		Generation:   generation,
		AnchorID:     s.eventStore.Head(),
		StartedAt:    startedAt,
		UpdatedAt:    startedAt,
		Status:       "running",
		Message:      "正在执行任务",
		LastEvent:    "started",
		TurnSequence: 1,
	}
	s.taskRuntimeLastWrite = time.Time{}
	s.taskRuntimeLastHash = ""
	return generation, s.persistTaskRuntimeLocked(true)
}

// CurrentTaskRuntimeGeneration returns the active in-process run token.
func (s *Service) CurrentTaskRuntimeGeneration() uint64 {
	if s == nil {
		return 0
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	return s.taskRuntime.Generation
}

func (s *Service) ensureTaskRuntimeGenerationLocked(taskID string) uint64 {
	if s.taskRuntime.Generation == 0 {
		s.taskRuntime.Generation = nextTaskRuntimeGeneration()
	}
	if s.taskRuntime.TaskID == "" {
		s.taskRuntime.TaskID = strings.TrimSpace(taskID)
		s.taskRuntime.AnchorID = s.eventStore.Head()
		s.taskRuntime.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.taskRuntime.Status = "running"
		s.taskRuntime.TurnSequence = 1
	}
	return s.taskRuntime.Generation
}

func taskRuntimeGenerationMatchesLocked(state TaskRuntimeState, taskID string, generation uint64) bool {
	return generation != 0 &&
		state.Generation == generation &&
		state.TaskID == strings.TrimSpace(taskID)
}

func taskRuntimeAcceptsStreamLocked(state TaskRuntimeState, taskID string, generation uint64) bool {
	return taskRuntimeGenerationMatchesLocked(state, taskID, generation) &&
		!state.Terminal() && strings.TrimSpace(state.Status) != "cancelling"
}

func (s *Service) taskRuntimeGenerationAcceptsTimeline(generation uint64) bool {
	if generation == 0 {
		// Timeline-only callers without a runtime retain legacy behavior.
		return true
	}
	if s == nil {
		return false
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	return s.taskRuntime.Generation == generation &&
		!s.taskRuntime.Terminal() && strings.TrimSpace(s.taskRuntime.Status) != "cancelling"
}

// MarkTaskRuntimeCancelling records that cancellation was requested. It does
// not claim that model streams, tools, subprocesses, or child agents have
// stopped; their owners must still perform and verify the actual cancellation.
func (s *Service) MarkTaskRuntimeCancelling(taskID string, generation uint64, message string) (bool, error) {
	if s == nil || s.eventStore == nil {
		return false, nil
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	if !taskRuntimeGenerationMatchesLocked(s.taskRuntime, taskID, generation) || s.taskRuntime.Terminal() {
		return false, nil
	}
	s.taskRuntime.Status = "cancelling"
	s.taskRuntime.LastEvent = "cancelling"
	if message = strings.TrimSpace(message); message != "" {
		s.taskRuntime.Message = redactSensitiveText(message)
	}
	return true, s.persistTaskRuntimeLocked(true)
}

// MarkTaskRuntimeInterrupted closes a generation after its cancellation work
// has actually finished or the caller has otherwise established interruption.
func (s *Service) MarkTaskRuntimeInterrupted(taskID string, generation uint64, message string) (bool, error) {
	if s == nil || s.eventStore == nil {
		return false, nil
	}
	s.taskRuntimeMu.Lock()
	defer s.taskRuntimeMu.Unlock()
	return s.finishTaskRuntimeLocked(taskID, generation, "interrupted", message, ChatResult{})
}
