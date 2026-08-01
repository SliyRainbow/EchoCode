package agent

import (
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// appendEvent is the single Agent-side entry point for durable events. The
// store still accepts legacy callers, while Agent events carry enough identity
// to reject stale generations and rebuild per-project views incrementally.
func (s *Service) appendEvent(payload eventlog.EventPayload, eventType eventlog.EventType, toolCallID ...string) (eventlog.Event, error) {
	if s == nil || s.eventStore == nil {
		return eventlog.Event{}, nil
	}
	header := eventlog.EventHeader{
		ProjectID: strings.TrimSpace(s.projectID),
		SessionID: strings.TrimSpace(s.sessionID),
		BranchID:  s.eventStore.BranchID(),
	}
	if snapshot, ok := s.TaskRuntimeSnapshot(); ok {
		header.RunID = strings.TrimSpace(snapshot.TaskID)
		header.Generation = int64(snapshot.Generation)
	}
	if len(toolCallID) > 0 {
		header.ToolCallID = strings.TrimSpace(toolCallID[0])
	}
	return s.eventStore.AppendWithHeader(header, payload, eventType)
}

func (s *Service) recordToolStartedEvent(name, toolCallID string, rawArgs []byte, startedAt time.Time) {
	if s == nil || s.eventStore == nil || strings.TrimSpace(name) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	payload := eventlog.ToolStartedPayload{Name: name}
	if err := payload.Validate(); err != nil {
		return
	}
	_, _ = s.appendEvent(eventlog.EventPayload{Parts: []eventlog.MessagePart{{
		Kind: string(tools.PartToolCall), Name: name, ToolCallID: toolCallID,
		Status: "running", Input: redactSensitiveText(toolInputForDisplay(name, rawArgs)),
		StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
	}}}, eventlog.EventToolStarted, toolCallID)
}

func (s *Service) recordToolTerminalEvent(name, toolCallID string, result tools.Result) {
	if s == nil || s.eventStore == nil || strings.TrimSpace(name) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	eventType := eventlog.EventToolCompleted
	if result.IsError {
		eventType = eventlog.EventToolFailed
	}
	part := safeToolEventPart(result, name, toolCallID)
	if result.IsError {
		payload := eventlog.ToolFailedPayload{Name: name, Error: redactSensitiveText(strings.TrimSpace(result.Summary)), DurationMs: part.DurationMs}
		if payload.Error == "" {
			payload.Error = "工具未返回可用结果"
		}
		if err := payload.Validate(); err != nil {
			return
		}
	} else if err := (eventlog.ToolCompletedPayload{Name: name, ExitCode: part.ExitCode, DurationMs: part.DurationMs}).Validate(); err != nil {
		return
	}
	_, _ = s.appendEvent(eventlog.EventPayload{Parts: []eventlog.MessagePart{part}}, eventType, toolCallID)
}

func safeToolEventPart(result tools.Result, name, toolCallID string) eventlog.MessagePart {
	part := eventlog.MessagePart{
		Kind: string(tools.PartToolCall), Name: strings.TrimSpace(name), ToolCallID: strings.TrimSpace(toolCallID),
		Status: "completed", Output: redactSensitiveText(strings.TrimSpace(result.Summary)),
	}
	if result.IsError {
		part.Status = "error"
	}
	for _, candidate := range result.Parts {
		if candidate.Kind != tools.PartToolCall || strings.TrimSpace(candidate.Name) != part.Name {
			continue
		}
		part.Status = strings.TrimSpace(candidate.Status)
		part.Input = redactSensitiveText(candidate.Input)
		part.Output = redactSensitiveText(candidate.Output)
		part.Stdout = redactSensitiveText(candidate.Stdout)
		part.Stderr = redactSensitiveText(candidate.Stderr)
		part.WorkingDirectory = candidate.WorkingDirectory
		part.ExitCode = candidate.ExitCode
		part.StartedAt = candidate.StartedAt
		part.CompletedAt = candidate.CompletedAt
		part.DurationMs = candidate.DurationMs
		break
	}
	if part.Status == "" {
		part.Status = "completed"
		if result.IsError {
			part.Status = "error"
		}
	}
	return part
}
