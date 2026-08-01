package agent

import (
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

const (
	subagentContextKind             = "subagent-context"
	maxSubagentStableContextRunes   = 32_000
	maxSubagentParentSummaryRunes   = 4_000
	maxSubagentRequestContextRunes  = 6_000
	maxSubagentArtifactContextRunes = 4_000
	maxSubagentFailureContextRunes  = 2_000
	maxSubagentExecutionRunes       = 3_000
	maxSubagentParentRequestRunes   = 4_000
)

// buildSubagentContextMessages produces a bounded handoff instead of copying
// the parent's complete conversation. Tool transcripts and old assistant turns
// remain owned by the parent; workers receive the stable rules, current task,
// selected durable context, and the latest user request.
func buildSubagentContextMessages(base []protocol.Message, spec delegateTaskSpec) []protocol.Message {
	var stableSystem *protocol.Message
	selected := make(map[string]protocol.Message, 5)
	var latestUser protocol.Message
	hasLatestUser := false

	for _, message := range base {
		if stableSystem == nil && message.Role == "system" && message.InternalKind == "" {
			cloned := minimalSubagentMessage(message, maxSubagentStableContextRunes)
			stableSystem = &cloned
		}
		switch message.InternalKind {
		case contextSummaryKind, contextRequestKind, contextArtifactKind, contextFailureStrategyKind, contextExecutionKind:
			selected[message.InternalKind] = minimalSubagentMessage(message, 0)
		}
		if message.Role == "user" && message.InternalKind == "" {
			latestUser = minimalSubagentMessage(message, maxSubagentParentRequestRunes)
			hasLatestUser = true
		}
	}

	messages := make([]protocol.Message, 0, 3)
	if stableSystem != nil && strings.TrimSpace(stableSystem.Content) != "" {
		messages = append(messages, *stableSystem)
	}

	sections := make([]string, 0, 6)
	appendSubagentContextSection(&sections, "parent_summary", selected[contextSummaryKind].Content, maxSubagentParentSummaryRunes)
	appendSubagentContextSection(&sections, "known_artifacts", selected[contextArtifactKind].Content, maxSubagentArtifactContextRunes)
	appendSubagentContextSection(&sections, "recent_failed_strategies", selected[contextFailureStrategyKind].Content, maxSubagentFailureContextRunes)
	appendSubagentContextSection(&sections, "interrupted_execution", selected[contextExecutionKind].Content, maxSubagentExecutionRunes)
	if hasLatestUser {
		appendSubagentContextSection(&sections, "parent_user_request", latestUser.Content, maxSubagentParentRequestRunes)
	}
	if len(sections) > 0 || hasLatestUser && len(latestUser.Attachments) > 0 {
		messages = append(messages, protocol.Message{
			Role: "user",
			Content: strings.Join([]string{
				"[EchoCode bounded subagent handoff]",
				"This is selected parent context, not a complete conversation transcript.",
				strings.Join(sections, "\n\n"),
				"[/EchoCode bounded subagent handoff]",
			}, "\n"),
			Attachments:  cloneSubagentAttachments(latestUser.Attachments),
			InternalKind: subagentContextKind,
		})
	}
	if requestContext := selected[contextRequestKind]; strings.TrimSpace(requestContext.Content) != "" {
		requestContext.Content = clipContextText(requestContext.Content, maxSubagentRequestContextRunes)
		requestContext.Attachments = nil
		messages = append(messages, requestContext)
	}

	messages = append(messages, protocol.Message{
		Role:    "user",
		Content: subagentInstruction(spec),
	})
	return messages
}

func appendSubagentContextSection(output *[]string, name, content string, maxRunes int) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	*output = append(*output, "["+name+"]\n"+clipContextText(content, maxRunes))
}

func minimalSubagentMessage(message protocol.Message, maxRunes int) protocol.Message {
	return protocol.Message{
		Role:         message.Role,
		Content:      clipContextText(message.Content, maxRunes),
		Attachments:  cloneSubagentAttachments(message.Attachments),
		InternalKind: message.InternalKind,
	}
}

func cloneSubagentAttachments(input []protocol.Attachment) []protocol.Attachment {
	if len(input) == 0 {
		return nil
	}
	return append([]protocol.Attachment(nil), input...)
}

func subagentInputSummary(base protocol.ChatRequest, spec delegateTaskSpec) string {
	parts := []string{"subtask: " + strings.TrimSpace(spec.Task)}
	for index := len(base.Messages) - 1; index >= 0; index-- {
		message := base.Messages[index]
		if message.Role != "user" || message.InternalKind != "" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		parts = append(parts, "parent request: "+compactContextLine(message.Content, maxSubagentParentRequestRunes))
		break
	}
	return clipContextText(redactSensitiveText(strings.Join(parts, "\n")), 6_000)
}
