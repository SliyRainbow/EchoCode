package protocol

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type sseEvent struct{ Type, Data string }

// SSE data can span several data: lines. Dispatch on the blank line, ignore
// comments/unknown fields, and bound the entire event as well as each line.
type sseReader struct {
	scanner *bufio.Scanner
	err     error
}

const maxSSEEventBytes = 4 * 1024 * 1024

func newSSEReader(reader io.Reader) *sseReader {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxSSEEventBytes)
	return &sseReader{scanner: scanner}
}

func (reader *sseReader) Next() (sseEvent, bool) {
	if reader.err != nil {
		return sseEvent{}, false
	}
	event := sseEvent{}
	var data []string
	size := 0
	for reader.scanner.Scan() {
		line := strings.TrimSuffix(reader.scanner.Text(), "\r")
		if line == "" {
			if len(data) > 0 {
				event.Data = strings.Join(data, "\n")
				return event, true
			}
			event = sseEvent{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event.Type = value
		case "data":
			size += len(value) + 1
			if size > maxSSEEventBytes {
				reader.err = fmt.Errorf("SSE event exceeds %d bytes", maxSSEEventBytes)
				return sseEvent{}, false
			}
			data = append(data, value)
		}
	}
	reader.err = reader.scanner.Err()
	if reader.err == nil && len(data) > 0 {
		event.Data = strings.Join(data, "\n")
		return event, true
	}
	return sseEvent{}, false
}

func (reader *sseReader) Err() error { return reader.err }

func streamFailure(provider, code, message string, retryable bool) StreamEvent {
	info := ProviderErrorInfo{Provider: provider, Type: "stream_error", Code: code, Message: message, Retryable: retryable}
	return StreamEvent{Type: "error", Error: message, ProviderError: &info}
}

func interruptedStream(provider string) StreamEvent {
	return streamFailure(provider, "unexpected_eof", "模型流式响应在结束事件前中断，已保留收到的内容。", true)
}

func finishReasonFailure(provider, reason string) *StreamEvent {
	var event StreamEvent
	switch reason {
	case "length", "max_tokens", "max_output_tokens":
		event = streamFailure(provider, "max_output_tokens", "模型输出达到上限，本轮尚未完整结束；已保留收到的内容。", false)
	case "content_filter":
		event = streamFailure(provider, "content_filter", "上游内容过滤中止了本轮输出，已保留收到的内容。", false)
	case "pause_turn":
		event = streamFailure(provider, "pause_turn", "上游暂停了本轮响应，需要继续处理；本轮未标记为完成。", false)
	default:
		return nil
	}
	return &event
}
