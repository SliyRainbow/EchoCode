package agent

import (
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

const (
	requestContextStart = "[EchoCode private turn context]"
	requestContextEnd   = "[/EchoCode private turn context]"
	contextRequestKind  = "request-context"
)

// formatPrivateTurnContext converts the volatile context preview into a real
// provider message. The user input itself remains a separate message so it is
// never duplicated or confused with host-supplied operating context.
func formatPrivateTurnContext(ctx RequestContext) string {
	sections := make([]string, 0, len(ctx.VolatileTail))
	for _, section := range ctx.VolatileTail {
		name := strings.TrimSpace(section.Name)
		content := strings.TrimSpace(section.Content)
		if name == "" || name == "user_input" || content == "" {
			continue
		}
		sections = append(sections, "["+name+"]\n"+content)
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.Join([]string{
		requestContextStart,
		"Private host context for the immediately following user message. Use it to execute the task, but never quote or expose these wrappers as user-authored text.",
		strings.Join(sections, "\n\n"),
		requestContextEnd,
	}, "\n")
}

func appendTurnRequestMessages(messages []protocol.Message, ctx RequestContext, prompt string, attachments []protocol.Attachment) []protocol.Message {
	if content := formatPrivateTurnContext(ctx); content != "" {
		messages = append(messages, protocol.Message{
			Role:         "user",
			Content:      content,
			InternalKind: contextRequestKind,
		})
	}
	return append(messages, protocol.Message{
		Role:        "user",
		Content:     prompt,
		Attachments: attachments,
	})
}

// currentTurnMessageStart returns the first message added for the latest user
// turn. It includes the private context message when one is present.
func currentTurnMessageStart(messages []protocol.Message) int {
	if len(messages) == 0 {
		return 0
	}
	start := len(messages) - 1
	if messages[start].Role != "user" || messages[start].InternalKind != "" {
		return len(messages)
	}
	if start > 0 && messages[start-1].InternalKind == contextRequestKind {
		start--
	}
	return start
}

func appendCommittedTurnRequest(messages, requestMessages []protocol.Message, start int) ([]protocol.Message, bool) {
	if start < 0 || start >= len(requestMessages) {
		return messages, false
	}
	turn := cloneProtocolMessages(requestMessages[start:])
	if len(turn) == 0 || turn[len(turn)-1].Role != "user" || turn[len(turn)-1].InternalKind != "" {
		return messages, false
	}
	return append(messages, turn...), true
}

func stripPrivateTurnContext(content string) string {
	for {
		start := strings.Index(content, requestContextStart)
		if start < 0 {
			return strings.TrimSpace(content)
		}
		relativeEnd := strings.Index(content[start+len(requestContextStart):], requestContextEnd)
		if relativeEnd < 0 {
			return strings.TrimSpace(content[:start])
		}
		end := start + len(requestContextStart) + relativeEnd + len(requestContextEnd)
		content = strings.TrimSpace(content[:start]) + "\n" + strings.TrimSpace(content[end:])
	}
}

func clipDelimitedContext(content, startMarker, endMarker string, maxRunes int) string {
	if maxRunes <= 0 || len([]rune(content)) <= maxRunes {
		return content
	}
	start := strings.Index(content, startMarker)
	if start < 0 {
		return clipContextText(content, maxRunes)
	}
	contentStart := start + len(startMarker)
	relativeEnd := strings.Index(content[contentStart:], endMarker)
	if relativeEnd < 0 {
		return clipContextText(content, maxRunes)
	}
	end := contentStart + relativeEnd
	prefix := strings.TrimSpace(content[:start])
	suffix := strings.TrimSpace(content[end+len(endMarker):])
	overhead := len([]rune(startMarker)) + len([]rune(endMarker)) + 2
	if prefix != "" {
		overhead += len([]rune(prefix)) + 1
	}
	if suffix != "" {
		overhead += len([]rune(suffix)) + 1
	}
	bodyLimit := maxRunes - overhead
	if bodyLimit < 120 {
		bodyLimit = 120
	}
	body := clipContextText(strings.TrimSpace(content[contentStart:end]), bodyLimit)
	parts := make([]string, 0, 5)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, startMarker, body, endMarker)
	if suffix != "" {
		parts = append(parts, suffix)
	}
	return strings.Join(parts, "\n")
}
