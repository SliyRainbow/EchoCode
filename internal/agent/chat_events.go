package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// ChatStreamEvent is the provider-independent progress contract used by the
// desktop task runner. Stable event names keep the UI independent of each API.
type ChatStreamEvent struct {
	Type            string                   `json:"type"`
	Delta           string                   `json:"delta,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Phase           string                   `json:"phase,omitempty"`
	ElapsedMs       int64                    `json:"elapsedMs,omitempty"`
	StageDurationMs int64                    `json:"stageDurationMs,omitempty"`
	Model           string                   `json:"model,omitempty"`
	ToolName        string                   `json:"toolName,omitempty"`
	ToolCallID      string                   `json:"toolCallId,omitempty"`
	ToolInput       string                   `json:"toolInput,omitempty"`
	Status          string                   `json:"status,omitempty"`
	Usage           *protocol.TokenUsage     `json:"usage,omitempty"`
	UsageState      *LiveUsageState          `json:"usageState,omitempty"`
	Progress        *tools.ResultPart        `json:"progress,omitempty"`
	Parts           []tools.ResultPart       `json:"parts,omitempty"`
	Compression     *ContextCompressionEvent `json:"compression,omitempty"`
	Team            *TeamRoleEvent           `json:"team,omitempty"`
}

// ContextCompressionEvent exposes automatic context compaction as a first-class
// task phase. The UI can show progress without parsing localized status text.
type ContextCompressionEvent struct {
	Status          string `json:"status"` // running | completed | error
	BeforeTokens    int    `json:"beforeTokens"`
	AfterTokens     int    `json:"afterTokens,omitempty"`
	RemovedMessages int    `json:"removedMessages,omitempty"`
	TargetTokens    int    `json:"targetTokens"`
}

type ChatEventSink func(ChatStreamEvent)

type providerStreamOpenResult struct {
	events <-chan protocol.StreamEvent
	err    error
}

const providerFinishGracePeriod = 1500 * time.Millisecond

const (
	providerStreamOpenTimeout = 45 * time.Second
	providerStreamIdleTimeout = 2 * time.Minute
	providerHeartbeatInterval = 8 * time.Second
)

var (
	ErrProviderStreamOpenTimeout = errors.New("provider stream did not open before the timeout")
	ErrProviderStreamIdle        = errors.New("provider stream became idle")
)

type providerStreamTiming struct {
	FinishGrace       time.Duration
	OpenTimeout       time.Duration
	IdleTimeout       time.Duration
	HeartbeatInterval time.Duration
}

func emitChatEvent(sink ChatEventSink, event ChatStreamEvent) {
	if sink != nil {
		sink(event)
	}
}

func serializedChatEventSink(sink ChatEventSink) ChatEventSink {
	if sink == nil {
		return nil
	}
	var mu sync.Mutex
	return func(event ChatStreamEvent) {
		mu.Lock()
		sink(event)
		mu.Unlock()
	}
}

func collectProviderStream(
	ctx context.Context,
	provider protocol.Provider,
	request protocol.ChatRequest,
	sink ChatEventSink,
) (protocol.CompletionResult, error) {
	return collectProviderStreamWithFinishGrace(ctx, provider, request, sink, providerFinishGracePeriod)
}

func collectProviderStreamWithFinishGrace(
	ctx context.Context,
	provider protocol.Provider,
	request protocol.ChatRequest,
	sink ChatEventSink,
	finishGrace time.Duration,
) (protocol.CompletionResult, error) {
	return collectProviderStreamWithTiming(ctx, provider, request, sink, providerStreamTiming{FinishGrace: finishGrace})
}

func collectProviderStreamWithTiming(
	ctx context.Context,
	provider protocol.Provider,
	request protocol.ChatRequest,
	sink ChatEventSink,
	timing providerStreamTiming,
) (protocol.CompletionResult, error) {
	if timing.FinishGrace <= 0 {
		timing.FinishGrace = providerFinishGracePeriod
	}
	if timing.OpenTimeout <= 0 {
		timing.OpenTimeout = providerStreamOpenTimeout
	}
	if timing.IdleTimeout <= 0 {
		timing.IdleTimeout = providerStreamIdleTimeout
	}
	if timing.HeartbeatInterval <= 0 {
		timing.HeartbeatInterval = providerHeartbeatInterval
	}
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	opened := make(chan providerStreamOpenResult, 1)
	go func() {
		events, err := provider.Stream(streamCtx, request)
		opened <- providerStreamOpenResult{events: events, err: err}
	}()

	var events <-chan protocol.StreamEvent
	openStartedAt := time.Now()
	streamStartedAt := openStartedAt
	firstProviderEvent := true
	firstTextEvent := true
	openTimer := time.NewTimer(timing.OpenTimeout)
	openHeartbeat := time.NewTicker(timing.HeartbeatInterval)
	openComplete := false
	for !openComplete {
		select {
		case <-ctx.Done():
			cancelStream()
			go drainLateProviderStream(opened)
			openTimer.Stop()
			openHeartbeat.Stop()
			return protocol.CompletionResult{}, ctx.Err()
		case <-openTimer.C:
			cancelStream()
			go drainLateProviderStream(opened)
			openHeartbeat.Stop()
			return protocol.CompletionResult{}, fmt.Errorf("%w after %s", ErrProviderStreamOpenTimeout, timing.OpenTimeout)
		case <-openHeartbeat.C:
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "heartbeat",
				Message: fmt.Sprintf("上游模型仍在建立连接（已等待 %s）", roundedWaitDuration(time.Since(openStartedAt))),
				Model:   request.Model,
				Status:  "waiting",
			})
		case result := <-opened:
			openTimer.Stop()
			openHeartbeat.Stop()
			if result.err != nil {
				return protocol.CompletionResult{}, result.err
			}
			if result.events == nil {
				return protocol.CompletionResult{}, errors.New("provider returned a nil event stream")
			}
			events = result.events
			emitProviderTimingEvent(ctx, sink, request.Model, "provider_connected", "模型连接已建立", openStartedAt, "completed")
			openComplete = true
		}
	}

	var content strings.Builder
	var reasoning strings.Builder
	result := protocol.CompletionResult{}
	partialResult := func() protocol.CompletionResult {
		result.Content = content.String()
		result.Reasoning = reasoning.String()
		return result
	}
	noticesSeen := make(map[string]bool)
	var finishTimer *time.Timer
	var finishDeadline <-chan time.Time
	finishSeen := false
	idleTimer := time.NewTimer(timing.IdleTimeout)
	heartbeat := time.NewTicker(timing.HeartbeatInterval)
	lastProviderEventAt := time.Now()
	stopFinishTimer := func() {
		if finishTimer != nil && !finishTimer.Stop() {
			select {
			case <-finishTimer.C:
			default:
			}
		}
	}
	defer stopFinishTimer()
	defer idleTimer.Stop()
	defer heartbeat.Stop()
	resetIdleTimer := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(timing.IdleTimeout)
		lastProviderEventAt = time.Now()
	}
	for {
		var event protocol.StreamEvent
		var ok bool
		select {
		case <-ctx.Done():
			cancelStream()
			go drainProviderStream(events)
			return partialResult(), ctx.Err()
		case <-idleTimer.C:
			cancelStream()
			go drainProviderStream(events)
			return partialResult(), fmt.Errorf("%w for %s", ErrProviderStreamIdle, timing.IdleTimeout)
		case <-heartbeat.C:
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "heartbeat",
				Message: fmt.Sprintf("上游模型仍在处理（%s 未收到新数据）", roundedWaitDuration(time.Since(lastProviderEventAt))),
				Model:   request.Model,
				Status:  "waiting",
			})
		case <-finishDeadline:
			// Some OpenAI-compatible relays send finish_reason but never send
			// [DONE] or close the response body. Keep a short window for the
			// trailing usage chunk, then treat the semantic finish as terminal.
			cancelStream()
			go drainProviderStream(events)
			return partialResult(), nil
		case event, ok = <-events:
			if !ok {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return partialResult(), ctxErr
				}
				return partialResult(), nil
			}
			resetIdleTimer()
			if firstProviderEvent {
				firstProviderEvent = false
				emitProviderTimingEvent(ctx, sink, request.Model, "provider_first_event", "已收到模型首个响应事件", streamStartedAt, "completed")
			}
		}
		switch event.Type {
		case "delta":
			if firstTextEvent {
				firstTextEvent = false
				emitProviderTimingEvent(ctx, sink, request.Model, "provider_first_text", "已收到模型首个文字输出", streamStartedAt, "completed")
			}
			content.WriteString(event.Delta)
			emitChatEvent(sink, ChatStreamEvent{Type: "delta", Delta: event.Delta})
		case "reasoning":
			reasoning.WriteString(event.Delta)
			emitChatEvent(sink, ChatStreamEvent{Type: "reasoning", Delta: event.Delta})
		case "tool_calls":
			result.ToolCalls = append(result.ToolCalls, event.ToolCalls...)
		case "continuation":
			result.Continuation = event.Continuation
		case "server_model":
			result.EffectiveModel = strings.TrimSpace(event.Delta)
		case "provider_notice":
			if event.Notice == nil {
				continue
			}
			key := providerNoticeIdentity(*event.Notice)
			if noticesSeen[key] {
				continue
			}
			noticesSeen[key] = true
			result.Notices = append(result.Notices, *event.Notice)
			part := providerNoticePart(*event.Notice)
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "provider_notice",
				Message: providerNoticeMessage(*event.Notice),
				Parts:   []tools.ResultPart{part},
			})
		case "usage":
			result.Usage = mergeTokenUsage(result.Usage, event.Usage)
			if result.Usage != nil {
				usage := *result.Usage
				emitChatEvent(sink, ChatStreamEvent{Type: "usage", Usage: &usage})
			}
		case "finish":
			if !finishSeen {
				finishSeen = true
				finishTimer = time.NewTimer(timing.FinishGrace)
				finishDeadline = finishTimer.C
			}
		case "done":
			return partialResult(), nil
		case "error":
			if ctxErr := ctx.Err(); ctxErr != nil {
				return partialResult(), ctxErr
			}
			if event.ProviderError != nil {
				return partialResult(), protocol.NewProviderError(*event.ProviderError)
			}
			if finishSeen {
				return partialResult(), nil
			}
			return partialResult(), errors.New(event.Error)
		}
	}
}

func roundedWaitDuration(duration time.Duration) time.Duration {
	if duration < time.Second {
		return time.Second
	}
	return duration.Round(time.Second)
}

func mergeTokenUsage(current, next *protocol.TokenUsage) *protocol.TokenUsage {
	if next == nil {
		return current
	}
	if current == nil {
		usage := *next
		if combined := usage.PromptTokens + usage.CompletionTokens; usage.TotalTokens < combined {
			usage.TotalTokens = combined
		}
		return &usage
	}
	merged := *current
	merged.PromptTokens = maxInt64(merged.PromptTokens, next.PromptTokens)
	merged.CompletionTokens = maxInt64(merged.CompletionTokens, next.CompletionTokens)
	merged.TotalTokens = maxInt64(merged.TotalTokens, next.TotalTokens)
	merged.PromptCacheHitTokens = maxInt64(merged.PromptCacheHitTokens, next.PromptCacheHitTokens)
	merged.PromptCacheMissTokens = maxInt64(merged.PromptCacheMissTokens, next.PromptCacheMissTokens)
	if combined := merged.PromptTokens + merged.CompletionTokens; merged.TotalTokens < combined {
		merged.TotalTokens = combined
	}
	return &merged
}

func maxInt64(left, right int64) int64 {
	if right > left {
		return right
	}
	return left
}

func providerNoticeParts(notices []protocol.ProviderNotice) []tools.ResultPart {
	if len(notices) == 0 {
		return nil
	}
	parts := make([]tools.ResultPart, 0, len(notices))
	seen := make(map[string]bool)
	for _, notice := range notices {
		key := providerNoticeIdentity(notice)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, providerNoticePart(notice))
	}
	return parts
}

func providerNoticePart(notice protocol.ProviderNotice) tools.ResultPart {
	return tools.ResultPart{
		Kind:           tools.PartProviderNotice,
		NoticeKind:     notice.Kind,
		Severity:       notice.Severity,
		Message:        notice.Message,
		RequestedModel: notice.RequestedModel,
		EffectiveModel: notice.EffectiveModel,
		RetryModel:     notice.RetryModel,
		UseCases:       append([]string(nil), notice.UseCases...),
		Reasons:        append([]string(nil), notice.Reasons...),
		Verifications:  append([]string(nil), notice.Verifications...),
		MetadataKeys:   append([]string(nil), notice.MetadataKeys...),
		RequestID:      notice.RequestID,
	}
}

func providerErrorNoticePart(info protocol.ProviderErrorInfo) tools.ResultPart {
	retryable := info.Retryable
	return tools.ResultPart{
		Kind:       tools.PartProviderNotice,
		NoticeKind: providerErrorNoticeKind(info),
		Severity:   "error",
		Message:    redactSensitiveText(info.Message),
		RequestID:  info.RequestID,
		ErrorCode:  info.Code,
		HTTPStatus: info.HTTPStatus,
		Retryable:  &retryable,
	}
}

func providerErrorNoticeKind(info protocol.ProviderErrorInfo) string {
	code := strings.ToLower(strings.TrimSpace(info.Code))
	typeName := strings.ToLower(strings.TrimSpace(info.Type))
	for _, value := range []string{code, typeName} {
		if strings.Contains(value, "policy") || strings.Contains(value, "safety") || strings.Contains(value, "moderation") {
			return protocol.ProviderNoticePolicyError
		}
	}
	if code == "bio_policy" {
		return protocol.ProviderNoticePolicyError
	}
	return protocol.ProviderNoticeProviderError
}

func providerNoticeIdentity(notice protocol.ProviderNotice) string {
	return strings.Join([]string{
		notice.Kind,
		notice.RequestedModel,
		notice.EffectiveModel,
		notice.RetryModel,
		strings.Join(notice.UseCases, ","),
		strings.Join(notice.Reasons, ","),
		strings.Join(notice.Verifications, ","),
		strings.Join(notice.MetadataKeys, ","),
	}, "\x00")
}

func providerNoticeMessage(notice protocol.ProviderNotice) string {
	if message := strings.TrimSpace(notice.Message); message != "" {
		return message
	}
	switch notice.Kind {
	case protocol.ProviderNoticeModelReroute:
		return fmt.Sprintf("服务端已将模型从 %s 路由到 %s", notice.RequestedModel, notice.EffectiveModel)
	case protocol.ProviderNoticeSafetyBuffering:
		return "服务端正在执行安全缓冲检查"
	case protocol.ProviderNoticeModelVerification:
		return "供应商返回了账户验证建议"
	case protocol.ProviderNoticeModeration:
		return "供应商返回了本轮内容安全元数据"
	default:
		return "供应商返回了运行时通知"
	}
}

func drainLateProviderStream(opened <-chan providerStreamOpenResult) {
	result := <-opened
	if result.err == nil && result.events != nil {
		drainProviderStream(result.events)
	}
}

func drainProviderStream(events <-chan protocol.StreamEvent) {
	for range events {
	}
}
