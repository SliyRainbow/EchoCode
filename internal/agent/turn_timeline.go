package agent

import (
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	maxTurnTimelineNotes    = 128
	timelineOverflowMessage = "较早的进展记录已折叠；完整工具结果仍保留在本轮活动记录中。"
)

func (s *Service) resetTurnTimeline() {
	s.turnTimelineMu.Lock()
	s.turnTimelineParts = nil
	s.turnTimelineMu.Unlock()
}

func (s *Service) captureTurnTimeline(sink ChatEventSink) ChatEventSink {
	generation := s.CurrentTaskRuntimeGeneration()
	return s.captureTurnTimelineForGeneration(generation, sink)
}

func (s *Service) captureTurnTimelineForGeneration(generation uint64, sink ChatEventSink) ChatEventSink {
	return func(event ChatStreamEvent) {
		if !s.taskRuntimeGenerationAcceptsTimeline(generation) {
			return
		}
		s.captureTurnTimelineEvent(event)
		emitChatEvent(sink, event)
	}
}

func (s *Service) captureTurnTimelineEvent(event ChatStreamEvent) {
	s.turnTimelineMu.Lock()
	defer s.turnTimelineMu.Unlock()

	switch event.Type {
	case "status", "context_compression":
		message := strings.TrimSpace(event.Message)
		if message == "" || isRoutineTaskStatus(message) {
			return
		}
		status := strings.TrimSpace(event.Status)
		if event.Type == "context_compression" && event.Compression != nil {
			status = event.Compression.Status
		}
		if status == "" {
			status = "running"
		}
		message = redactSensitiveText(message)
		for index := len(s.turnTimelineParts) - 1; index >= 0; index-- {
			part := s.turnTimelineParts[index]
			if part.Kind != tools.PartTimelineNote {
				continue
			}
			if event.ToolCallID != "" && part.ToolCallID == event.ToolCallID ||
				event.ToolCallID == "" && part.Message == message {
				s.turnTimelineParts[index] = mergeTimelineNoteParts(part, tools.ResultPart{
					Kind: tools.PartTimelineNote, Message: message, Status: status,
					ToolCallID: strings.TrimSpace(event.ToolCallID),
				})
				return
			}
		}
		settleOpenTimelineNotes(s.turnTimelineParts, "completed")
		if timelineNoteCount(s.turnTimelineParts) >= maxTurnTimelineNotes-1 {
			appendTimelineOverflowNote(&s.turnTimelineParts)
			return
		}
		s.turnTimelineParts = append(s.turnTimelineParts, tools.ResultPart{
			Kind: tools.PartTimelineNote, Message: message, Status: status,
			ToolCallID: strings.TrimSpace(event.ToolCallID),
			StartedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		})
	case "tool":
		settleOpenTimelineNotes(s.turnTimelineParts, "completed")
		state := TaskRuntimeState{Parts: s.turnTimelineParts}
		upsertTaskRuntimeToolPart(&state, event)
		state.Parts = mergeTaskRuntimeParts(state.Parts, redactTaskRuntimeParts(event.Parts))
		s.turnTimelineParts = state.Parts
	case "progress":
		if event.Progress != nil {
			s.turnTimelineParts = mergeTaskRuntimeParts(s.turnTimelineParts, redactTaskRuntimeParts([]tools.ResultPart{*event.Progress}))
		}
	case "team":
		if event.Team != nil {
			s.turnTimelineParts = mergeTaskRuntimeParts(s.turnTimelineParts, []tools.ResultPart{taskRuntimeTeamPart(*event.Team)})
		}
	case "subagent", "provider_notice":
		s.turnTimelineParts = mergeTaskRuntimeParts(s.turnTimelineParts, redactTaskRuntimeParts(event.Parts))
	}
}

func appendTimelineOverflowNote(parts *[]tools.ResultPart) {
	if parts == nil {
		return
	}
	for _, part := range *parts {
		if part.Kind == tools.PartTimelineNote && part.Message == timelineOverflowMessage {
			return
		}
	}
	marker := tools.ResultPart{
		Kind: tools.PartTimelineNote, Message: timelineOverflowMessage, Status: "completed",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if timelineNoteCount(*parts) >= maxTurnTimelineNotes {
		for index := len(*parts) - 1; index >= 0; index-- {
			if (*parts)[index].Kind == tools.PartTimelineNote {
				(*parts)[index] = marker
				return
			}
		}
	}
	*parts = append(*parts, marker)
}

func settleOpenTimelineNotes(parts []tools.ResultPart, status string) {
	if status == "" {
		status = "completed"
	}
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	for index := range parts {
		if parts[index].Kind != tools.PartTimelineNote || taskRuntimePartTerminal(parts[index].Status) {
			continue
		}
		parts[index].Status = status
		parts[index].CompletedAt = completedAt
	}
}

func isRoutineTaskStatus(message string) bool {
	message = strings.TrimSpace(message)
	return message == "正在执行任务" ||
		message == "正在准备上下文" ||
		message == "正在分析任务" ||
		message == "正在生成执行计划" ||
		strings.HasPrefix(message, "正在连接 ") ||
		strings.HasPrefix(message, "上游模型仍在处理")
}

func (s *Service) mergeTurnTimelineParts(parts []tools.ResultPart, content, terminalStatus string) []tools.ResultPart {
	s.turnTimelineMu.Lock()
	timeline := redactTaskRuntimeParts(s.turnTimelineParts)
	s.turnTimelineMu.Unlock()
	if len(timeline) == 0 {
		return parts
	}

	for index := range timeline {
		if timeline[index].Kind != tools.PartTimelineNote {
			continue
		}
		if !taskRuntimePartTerminal(timeline[index].Status) {
			timeline[index].Status = terminalStatus
			timeline[index].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	merged := mergeTaskRuntimeParts(timeline, redactTaskRuntimeParts(parts))
	return appendTextPartIfMissing(merged, sanitizeModelContent(content))
}

func lastTimelineNote(parts []tools.ResultPart) *tools.ResultPart {
	for index := len(parts) - 1; index >= 0; index-- {
		if parts[index].Kind == tools.PartTimelineNote {
			return &parts[index]
		}
	}
	return nil
}

func timelineNoteCount(parts []tools.ResultPart) int {
	count := 0
	for _, part := range parts {
		if part.Kind == tools.PartTimelineNote {
			count++
		}
	}
	return count
}
