package protocol

import (
	"encoding/json"
	"sort"
	"strings"
)

// streamToolCallDelta mirrors the OpenAI-compatible incremental tool-call shape.
// DeepSeek uses the same wire format.
type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type streamToolCallAccumulator struct {
	ID        string
	Type      string
	Name      strings.Builder
	Arguments strings.Builder
}

func accumulateToolCallDeltas(accumulators map[int]*streamToolCallAccumulator, deltas []streamToolCallDelta) {
	for _, delta := range deltas {
		current := accumulators[delta.Index]
		if current == nil {
			current = &streamToolCallAccumulator{}
			accumulators[delta.Index] = current
		}
		if delta.ID != "" {
			current.ID = delta.ID
		}
		if delta.Type != "" {
			current.Type = delta.Type
		}
		current.Name.WriteString(delta.Function.Name)
		current.Arguments.WriteString(delta.Function.Arguments)
	}
}

func emitAccumulatedToolCalls(events chan<- StreamEvent, accumulators map[int]*streamToolCallAccumulator) {
	if len(accumulators) == 0 {
		return
	}
	indexes := make([]int, 0, len(accumulators))
	for index := range accumulators {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	calls := make([]ToolCall, 0, len(indexes))
	for _, index := range indexes {
		item := accumulators[index]
		arguments := strings.TrimSpace(item.Arguments.String())
		if arguments == "" || !json.Valid([]byte(arguments)) {
			arguments = "{}"
		}
		callType := item.Type
		if callType == "" {
			callType = "function"
		}
		calls = append(calls, ToolCall{
			ID:   item.ID,
			Type: callType,
			Function: ToolCallFunction{
				Name:      item.Name.String(),
				Arguments: json.RawMessage(arguments),
			},
		})
	}
	events <- StreamEvent{Type: "tool_calls", ToolCalls: calls}
}
