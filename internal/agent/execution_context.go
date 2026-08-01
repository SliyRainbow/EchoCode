package agent

import (
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	executionContextStart  = "[EchoCode execution checkpoint]"
	executionContextEnd    = "[/EchoCode execution checkpoint]"
	contextExecutionKind   = "execution-context"
	terminalTurnKindPrefix = "terminal-turn:"

	executionCheckpointRuneLimit        = 8_000
	executionCheckpointDetailRuneBudget = 6_000
	executionCheckpointEntryRuneLimit   = 2_400
)

func (s *Service) formatExecutionCheckpoint(parts []tools.ResultPart) string {
	lines := make([]string, 0, 24)
	if plan := formatPlanCheckpoint(s.planState); plan != "" {
		lines = append(lines, plan)
	}
	if team := formatTeamCheckpoint(s.teamResume); team != "" {
		lines = append(lines, team)
	}

	details := make([]string, 0, len(parts))
	remaining := executionCheckpointDetailRuneBudget
	omitted := 0
	for index := len(parts) - 1; index >= 0; index-- {
		detail := formatExecutionCheckpointPart(parts[index])
		if detail == "" {
			continue
		}
		detail = clipContextText(detail, executionCheckpointEntryRuneLimit)
		cost := len([]rune(detail)) + 1
		if cost > remaining {
			omitted++
			continue
		}
		details = append(details, detail)
		remaining -= cost
	}
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("older_operational_entries_omitted=%d; latest entries retained within checkpoint budget", omitted))
	}
	for index := len(details) - 1; index >= 0; index-- {
		lines = append(lines, details[index])
	}
	if len(lines) == 0 {
		return ""
	}
	content := strings.Join([]string{
		executionContextStart,
		"Branch-local operational facts from prior work. Decide from the current user request whether it continues this work. When it does, reuse successful results and do not repeat a failed strategy without a substantive change. When it does not, treat these facts only as background and do not resume work automatically.",
		strings.Join(lines, "\n"),
		executionContextEnd,
	}, "\n")
	return clipDelimitedContext(content, executionContextStart, executionContextEnd, executionCheckpointRuneLimit)
}

func formatExecutionCheckpointPart(part tools.ResultPart) string {
	switch part.Kind {
	case tools.PartToolCall:
		return formatToolCheckpoint(part)
	case tools.PartProgress:
		return formatProgressCheckpoint(part)
	case tools.PartSubagent:
		return fmt.Sprintf(
			"subagent task_id=%s type=%s status=%s label=%q action=%q",
			checkpointValue(part.TaskID), checkpointValue(part.AgentType), checkpointValue(part.Status),
			checkpointText(part.Label, 300), checkpointText(part.CurrentAction, 500),
		)
	case tools.PartTeamRole:
		return fmt.Sprintf(
			"team_role role=%s attempt=%d status=%s verdict=%s summary=%q",
			checkpointValue(part.Role), part.Attempt, checkpointValue(part.Status), checkpointValue(part.Verdict),
			checkpointText(part.Summary, 800),
		)
	default:
		return ""
	}
}

func formatPlanCheckpoint(plan PlanState) string {
	if len(plan.Steps) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "plan revision=%d status=%s", plan.Revision, checkpointValue(plan.Status))
	for index, step := range plan.Steps {
		fmt.Fprintf(&out, "\n  step=%d status=%s title=%q", index+1, checkpointValue(step.Status), checkpointText(step.Title, 500))
	}
	return out.String()
}

func formatTeamCheckpoint(checkpoint *teamRunCheckpoint) string {
	if checkpoint == nil {
		return ""
	}
	return fmt.Sprintf(
		"team run_id=%s status=%s next_role=%s next_attempt=%d review_round=%d completed_roles=%d",
		checkpointValue(checkpoint.Team.RunID), checkpointValue(checkpoint.Status), checkpointValue(checkpoint.NextRole),
		checkpoint.NextAttempt, checkpoint.ReviewRound, len(checkpoint.Artifacts),
	)
}

func formatToolCheckpoint(part tools.ResultPart) string {
	var out strings.Builder
	fmt.Fprintf(&out, "tool name=%s status=%s call_id=%s", checkpointValue(part.Name), checkpointValue(part.Status), checkpointValue(part.ToolCallID))
	if part.WorkingDirectory != "" {
		fmt.Fprintf(&out, " cwd=%q", checkpointText(part.WorkingDirectory, 800))
	}
	if part.ExitCode != nil {
		fmt.Fprintf(&out, " exit_code=%d", *part.ExitCode)
	}
	if part.DurationMs > 0 {
		fmt.Fprintf(&out, " duration_ms=%d", part.DurationMs)
	}
	appendCheckpointField(&out, "input", part.Input, 2_000)
	appendCheckpointField(&out, "stdout", part.Stdout, 1_500)
	appendCheckpointField(&out, "stderr", part.Stderr, 2_000)
	if strings.TrimSpace(part.Stdout) == "" && strings.TrimSpace(part.Stderr) == "" {
		appendCheckpointField(&out, "output", part.Output, 1_500)
	}
	return out.String()
}

func formatProgressCheckpoint(part tools.ResultPart) string {
	if len(part.Steps) == 0 && strings.TrimSpace(part.TaskStatus) == "" {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "task status=%s changed_files=%d", checkpointValue(part.TaskStatus), part.ChangedFiles)
	for index, step := range part.Steps {
		fmt.Fprintf(&out, "\n  step=%d status=%s title=%q", index+1, checkpointValue(step.Status), checkpointText(step.Title, 500))
	}
	return out.String()
}

func appendCheckpointField(out *strings.Builder, name, value string, maxRunes int) {
	value = checkpointText(value, maxRunes)
	if value == "" {
		return
	}
	fmt.Fprintf(out, "\n  %s=%q", name, value)
}

func checkpointText(value string, maxRunes int) string {
	value = redactSensitiveText(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return clipContextText(value, maxRunes)
}

func checkpointValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func latestResumableExecutionCheckpoint(messages []protocol.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "assistant" {
			continue
		}
		// A terminal checkpoint is useful only until the next completed assistant
		// turn. Looking farther back would keep feeding an abandoned task into
		// unrelated work long after the user moved on.
		if !isResumableTerminalAssistant(message) {
			return ""
		}
		return extractExecutionCheckpoint(message.Content)
	}
	return ""
}

func extractExecutionCheckpoint(content string) string {
	start := strings.LastIndex(content, executionContextStart)
	if start < 0 {
		return ""
	}
	relativeEnd := strings.Index(content[start+len(executionContextStart):], executionContextEnd)
	if relativeEnd < 0 {
		return ""
	}
	end := start + len(executionContextStart) + relativeEnd + len(executionContextEnd)
	return strings.TrimSpace(content[start:end])
}

func stripExecutionCheckpoint(content string) string {
	for {
		start := strings.Index(content, executionContextStart)
		if start < 0 {
			return strings.TrimSpace(content)
		}
		relativeEnd := strings.Index(content[start+len(executionContextStart):], executionContextEnd)
		if relativeEnd < 0 {
			return strings.TrimSpace(content[:start])
		}
		end := start + len(executionContextStart) + relativeEnd + len(executionContextEnd)
		content = strings.TrimSpace(content[:start]) + "\n" + strings.TrimSpace(content[end:])
	}
}

func (s *Service) continuationExecutionContextLocked(_ string) string {
	if checkpoint := latestResumableExecutionCheckpoint(s.sessionMessages); checkpoint != "" {
		return checkpoint
	}
	if s.hasResumableExecutionStateLocked() {
		return s.formatExecutionCheckpoint(nil)
	}
	return ""
}

// hasResumableExecutionState intentionally checks durable state rather than
// parsing the user's wording. The model receives prior operational facts and
// decides whether the current request continues that work.
func (s *Service) hasResumableExecutionStateLocked() bool {
	if hasActivePlanState(s.planState) {
		return true
	}
	if s.hasPausedTeamRunLocked() {
		return true
	}
	if latestResumableExecutionCheckpoint(s.sessionMessages) != "" {
		return true
	}
	runtime, ok := s.TaskRuntimeSnapshot()
	if !ok {
		return false
	}
	switch strings.TrimSpace(runtime.Status) {
	case "running", "waiting", "retrying", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func hasActivePlanState(plan PlanState) bool {
	if len(plan.Steps) == 0 {
		return false
	}
	switch strings.TrimSpace(plan.Status) {
	case "running", "waiting", "retrying":
		return true
	default:
		return false
	}
}

func terminalTurnInternalKind(status string) string {
	return terminalTurnKindPrefix + strings.TrimSpace(status)
}

func isResumableTerminalAssistant(message protocol.Message) bool {
	if message.Role != "assistant" {
		return false
	}
	switch strings.TrimPrefix(strings.TrimSpace(message.InternalKind), terminalTurnKindPrefix) {
	case "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}
