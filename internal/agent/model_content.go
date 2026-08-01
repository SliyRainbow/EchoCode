package agent

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

var (
	privateReasoningBlockPattern = regexp.MustCompile(`(?is)<(?:thinking|think|analysis|reasoning)\b[^>]*>.*?</(?:thinking|think|analysis|reasoning)\s*>[\t ]*(?:\r?\n)?`)
	privateReasoningTailPattern  = regexp.MustCompile(`(?is)<(?:thinking|think|analysis|reasoning)\b[^>]*>.*\z`)
	privateReasoningEndPattern   = regexp.MustCompile(`(?is)</(?:thinking|think|analysis|reasoning)\s*>[\t ]*(?:\r?\n)?`)
)

// stripTaggedPrivateReasoning handles compatibility endpoints that place
// private reasoning in content instead of their dedicated reasoning field.
func stripTaggedPrivateReasoning(content string) string {
	for {
		next := privateReasoningBlockPattern.ReplaceAllString(content, "")
		if next == content {
			break
		}
		content = next
	}
	content = privateReasoningTailPattern.ReplaceAllString(content, "")
	content = privateReasoningEndPattern.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}

func visibleCompletionContent(content string, calls []protocol.ToolCall) string {
	content = sanitizeModelContent(content)
	if content == "" || (!duplicatesToolArguments(content, calls) && !isProgressToolPayload(content)) {
		return content
	}
	return ""
}

func isProgressToolPayload(content string) bool {
	var value map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &value) != nil || len(value) == 0 {
		return false
	}
	for key := range value {
		if key != "message" && key != "status" {
			return false
		}
	}
	if _, ok := value["message"].(string); !ok {
		return false
	}
	status, _ := value["status"].(string)
	switch strings.TrimSpace(status) {
	case "", "running", "waiting", "retrying":
		return true
	default:
		return false
	}
}

func duplicatesToolArguments(content string, calls []protocol.ToolCall) bool {
	var visible any
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &visible) != nil {
		return false
	}
	for _, call := range calls {
		var arguments any
		if json.Unmarshal(call.Function.Arguments, &arguments) == nil && reflect.DeepEqual(visible, arguments) {
			return true
		}
	}
	return false
}
