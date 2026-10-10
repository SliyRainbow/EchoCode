package protocol

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestResponsesReasoningTextStaysOutOfAnswer(t *testing.T) {
	const output = `{"status":"completed","output":[{"type":"reasoning","id":"rs-1","content":[{"type":"reasoning_text","text":"Inspect files"}],"summary":[{"type":"summary_text","text":"Brief"}],"encrypted_content":"opaque"},{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Tool ready."}]},{"type":"function_call","call_id":"call-list","name":"list_dir","arguments":"{}"}]}`
	const item = `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs-1","content":[{"type":"reasoning_text","text":"Inspect files"}],"summary":[{"type":"summary_text","text":"Brief"}],"encrypted_content":"opaque"}}`
	var response responsesResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, result CompletionResult) {
		t.Helper()
		if result.Content != "Tool ready." || result.Reasoning != "Inspect files\n\nBrief" || len(result.ToolCalls) != 1 {
			t.Fatalf("reasoning/answer separation failed: %#v", result)
		}
		_, input := responsesInputFromProtocol([]Message{{Role: "assistant", Content: result.Content, ToolCalls: result.ToolCalls, Continuation: result.Continuation}})
		if len(input) != 3 || input[0].Type != "reasoning" || len(input[0].Content) != 1 || input[0].Content[0].Type != "reasoning_text" || input[0].Content[0].Text != "Inspect files" || input[0].EncryptedContent != "opaque" {
			t.Fatalf("reasoning continuation lost: %#v", input)
		}
		if input[1].Content[0].Text != "Tool ready." || input[1].Phase != "commentary" {
			t.Fatalf("reasoning leaked into replayed assistant text: %#v", input[1])
		}
	}
	t.Run("complete", func(t *testing.T) { check(t, completionFromResponses(response)) })
	for _, test := range []struct {
		name   string
		frames []string
	}{
		{"deltas-and-snapshots", []string{
			`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"Inspect "}`,
			`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"files"}`,
			`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"Inspect files"}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Brief"}`,
			`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"Brief"}`,
			item,
		}},
		{"done-and-snapshot", []string{`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"Inspect files"}`, item}},
		{"item-only", []string{item}},
		{"completed-only", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := append(test.frames, `{"type":"response.completed","response":`+output+`}`)
			for i := range frames {
				frames[i] = "data: " + frames[i]
			}
			events := auditStreamEvents(func(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
				parseResponsesStream(ctx, body, events, "test-model", responsesTransportMetadata{})
			}, strings.Join(frames, "\n\n")+"\n\n")
			var result CompletionResult
			for _, event := range events {
				switch event.Type {
				case "delta":
					result.Content += event.Delta
				case "reasoning":
					result.Reasoning += event.Delta
				case "tool_calls":
					result.ToolCalls = append(result.ToolCalls, event.ToolCalls...)
				case "continuation":
					result.Continuation = event.Continuation
				case "error":
					t.Error(event.Error)
				}
			}
			check(t, result)
			if test.name == "deltas-and-snapshots" && (len(events) == 0 || events[0].Type != "reasoning" || events[0].Delta != "Inspect ") {
				t.Fatal("reasoning must stream before the final snapshot")
			}
		})
	}
}
