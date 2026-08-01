package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type stalledStreamProvider struct {
	started chan struct{}
	events  chan protocol.StreamEvent
}

type stalledOpenProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *stalledOpenProvider) Name() string { return "stalled-open" }

func (p *stalledOpenProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return nil, nil
}

func (p *stalledOpenProvider) Stream(context.Context, protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	close(p.started)
	<-p.release
	events := make(chan protocol.StreamEvent)
	close(events)
	return events, nil
}

func (p *stalledStreamProvider) Name() string { return "stalled" }

func (p *stalledStreamProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return nil, nil
}

func (p *stalledStreamProvider) Stream(context.Context, protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	close(p.started)
	return p.events, nil
}

func TestCollectProviderStreamStopsWhileEventStreamIsStillOpen(t *testing.T) {
	provider := &stalledStreamProvider{
		started: make(chan struct{}),
		events:  make(chan protocol.StreamEvent),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := collectProviderStream(ctx, provider, protocol.ChatRequest{}, nil)
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collect error = %v, want context cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("collectProviderStream did not stop promptly")
	}
	close(provider.events)
}

func TestCollectProviderStreamReturnsTextReceivedBeforeCancellation(t *testing.T) {
	provider := &stalledStreamProvider{
		started: make(chan struct{}),
		events:  make(chan protocol.StreamEvent),
	}
	ctx, cancel := context.WithCancel(context.Background())
	type streamResult struct {
		completion protocol.CompletionResult
		err        error
	}
	done := make(chan streamResult, 1)
	go func() {
		completion, err := collectProviderStream(ctx, provider, protocol.ChatRequest{}, nil)
		done <- streamResult{completion: completion, err: err}
	}()

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}
	provider.events <- protocol.StreamEvent{Type: "delta", Delta: "partial answer"}
	cancel()

	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("collect error = %v, want context cancellation", result.err)
		}
		if result.completion.Content != "partial answer" {
			t.Fatalf("partial content = %q", result.completion.Content)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("collectProviderStream did not return partial content promptly")
	}
	close(provider.events)
}

func TestCollectProviderStreamStopsWhileProviderOpenIsBlocked(t *testing.T) {
	provider := &stalledOpenProvider{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := collectProviderStream(ctx, provider, protocol.ChatRequest{}, nil)
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider open did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collect error = %v, want context cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked provider open prevented prompt cancellation")
	}
	close(provider.release)
}

func TestCollectProviderStreamTimesOutWhileProviderOpenIsBlocked(t *testing.T) {
	provider := &stalledOpenProvider{started: make(chan struct{}), release: make(chan struct{})}
	startedAt := time.Now()
	result, err := collectProviderStreamWithTiming(
		context.Background(),
		provider,
		protocol.ChatRequest{Model: "slow-open"},
		nil,
		providerStreamTiming{
			OpenTimeout:       35 * time.Millisecond,
			IdleTimeout:       time.Second,
			HeartbeatInterval: 10 * time.Millisecond,
		},
	)
	close(provider.release)
	if !errors.Is(err, ErrProviderStreamOpenTimeout) {
		t.Fatalf("open timeout error = %v", err)
	}
	if result.Content != "" {
		t.Fatalf("open timeout content = %q", result.Content)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("open timeout took %s", elapsed)
	}
}

func TestCollectProviderStreamIdleTimeoutKeepsPartialOutputAndEmitsHeartbeat(t *testing.T) {
	events := make(chan protocol.StreamEvent, 1)
	events <- protocol.StreamEvent{Type: "delta", Delta: "partial answer"}
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}
	var heartbeatEvents []ChatStreamEvent
	result, err := collectProviderStreamWithTiming(
		context.Background(),
		provider,
		protocol.ChatRequest{Model: "slow-stream"},
		func(event ChatStreamEvent) {
			if event.Type == "heartbeat" {
				heartbeatEvents = append(heartbeatEvents, event)
			}
		},
		providerStreamTiming{
			OpenTimeout:       time.Second,
			IdleTimeout:       55 * time.Millisecond,
			HeartbeatInterval: 15 * time.Millisecond,
		},
	)
	close(events)
	if !errors.Is(err, ErrProviderStreamIdle) {
		t.Fatalf("idle timeout error = %v", err)
	}
	if result.Content != "partial answer" {
		t.Fatalf("partial content = %q", result.Content)
	}
	if len(heartbeatEvents) == 0 || heartbeatEvents[0].Status != "waiting" || heartbeatEvents[0].Model != "slow-stream" {
		t.Fatalf("heartbeat events = %#v", heartbeatEvents)
	}
}

func TestCollectProviderStreamActivityResetsIdleTimeout(t *testing.T) {
	events := make(chan protocol.StreamEvent)
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}
	go func() {
		defer close(events)
		for _, value := range []string{"one", "two", "three"} {
			events <- protocol.StreamEvent{Type: "delta", Delta: value}
			time.Sleep(20 * time.Millisecond)
		}
		events <- protocol.StreamEvent{Type: "done"}
	}()
	result, err := collectProviderStreamWithTiming(
		context.Background(),
		provider,
		protocol.ChatRequest{Model: "active-stream"},
		nil,
		providerStreamTiming{
			OpenTimeout:       time.Second,
			IdleTimeout:       45 * time.Millisecond,
			HeartbeatInterval: 10 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "onetwothree" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestCollectProviderStreamStopsAfterSemanticFinishWithoutEOF(t *testing.T) {
	events := make(chan protocol.StreamEvent, 2)
	events <- protocol.StreamEvent{Type: "delta", Delta: "finished answer"}
	events <- protocol.StreamEvent{Type: "finish", FinishReason: "stop"}
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}

	started := time.Now()
	result, err := collectProviderStreamWithFinishGrace(
		context.Background(), provider, protocol.ChatRequest{}, nil, 25*time.Millisecond,
	)
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "finished answer" {
		t.Fatalf("content = %q", result.Content)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("semantic finish waited too long: %s", elapsed)
	}
}

func TestCollectProviderStreamKeepsTrailingUsageUntilDone(t *testing.T) {
	events := make(chan protocol.StreamEvent, 6)
	events <- protocol.StreamEvent{Type: "delta", Delta: "done"}
	events <- protocol.StreamEvent{Type: "finish", FinishReason: "stop"}
	events <- protocol.StreamEvent{Type: "usage", Usage: &protocol.TokenUsage{
		PromptTokens: 100, PromptCacheHitTokens: 80, PromptCacheMissTokens: 20,
	}}
	events <- protocol.StreamEvent{Type: "usage", Usage: &protocol.TokenUsage{CompletionTokens: 12}}
	events <- protocol.StreamEvent{Type: "done"}
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}

	result, err := collectProviderStreamWithFinishGrace(
		context.Background(), provider, protocol.ChatRequest{}, nil, time.Second,
	)
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 100 || result.Usage.CompletionTokens != 12 || result.Usage.TotalTokens != 112 {
		t.Fatalf("merged usage = %#v", result.Usage)
	}
}

func TestCollectProviderStreamCancelsHTTPStreamAfterFinishWithoutDone(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished answer\"},\"finish_reason\":\"stop\"}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	provider := protocol.OpenAICompatibleProvider{
		BaseURL: server.URL, APIKey: "sk-test", HTTPClient: server.Client(),
	}
	started := time.Now()
	result, err := collectProviderStreamWithFinishGrace(context.Background(), provider, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "ping"}},
	}, nil, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "finished answer" {
		t.Fatalf("content = %q", result.Content)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("semantic finish waited too long: %s", elapsed)
	}
	select {
	case <-requestCanceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("finished HTTP request was not canceled")
	}
}

func TestCollectProviderStreamKeepsHTTPUsageBeforeCancelingStalledStream(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"total_tokens\":100}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":12,\"total_tokens\":12}}\n\n")
		flusher.Flush()
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	provider := protocol.OpenAICompatibleProvider{
		BaseURL: server.URL, APIKey: "sk-test", HTTPClient: server.Client(),
	}
	result, err := collectProviderStreamWithFinishGrace(context.Background(), provider, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "ping"}},
	}, nil, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 100 || result.Usage.CompletionTokens != 12 || result.Usage.TotalTokens != 112 {
		t.Fatalf("merged HTTP usage = %#v", result.Usage)
	}
	select {
	case <-requestCanceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stalled usage stream was not canceled")
	}
}

func TestCollectProviderStreamReturnsImmediatelyOnHTTPDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := protocol.OpenAICompatibleProvider{
		BaseURL: server.URL, APIKey: "sk-test", HTTPClient: server.Client(),
	}
	started := time.Now()
	result, err := collectProviderStreamWithFinishGrace(context.Background(), provider, protocol.ChatRequest{
		Model: "test-model", Messages: []protocol.Message{{Role: "user", Content: "ping"}},
	}, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("[DONE] waited for finish grace period: %s", elapsed)
	}
}

func TestCollectProviderStreamPreservesTypedProviderErrorAfterFinish(t *testing.T) {
	events := make(chan protocol.StreamEvent, 2)
	events <- protocol.StreamEvent{Type: "finish", FinishReason: "stop"}
	events <- protocol.StreamEvent{
		Type: "error", Error: "request blocked",
		ProviderError: &protocol.ProviderErrorInfo{Code: "cyber_policy", Message: "request blocked", Retryable: false},
	}
	close(events)
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}
	_, err := collectProviderStreamWithFinishGrace(context.Background(), provider, protocol.ChatRequest{}, nil, time.Second)
	info, ok := protocol.ProviderErrorDetails(err)
	if !ok || info.Code != "cyber_policy" || info.Retryable {
		t.Fatalf("error = %v, info = %#v, ok = %v", err, info, ok)
	}
}

func TestCollectProviderStreamEmitsAndCollectsProviderNotice(t *testing.T) {
	notice := protocol.ProviderNotice{
		Kind: protocol.ProviderNoticeModelReroute, Severity: "warning",
		RequestedModel: "gpt-requested", EffectiveModel: "gpt-restricted", RequestID: "req-1",
	}
	events := make(chan protocol.StreamEvent, 4)
	events <- protocol.StreamEvent{Type: "server_model", Delta: "gpt-restricted"}
	events <- protocol.StreamEvent{Type: "provider_notice", Notice: &notice}
	events <- protocol.StreamEvent{Type: "delta", Delta: "done"}
	events <- protocol.StreamEvent{Type: "done"}
	close(events)
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}
	var emitted []ChatStreamEvent
	result, err := collectProviderStream(context.Background(), provider, protocol.ChatRequest{}, func(event ChatStreamEvent) {
		emitted = append(emitted, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || result.EffectiveModel != "gpt-restricted" || len(result.Notices) != 1 {
		t.Fatalf("result = %#v", result)
	}
	found := false
	for _, event := range emitted {
		if event.Type == "provider_notice" && len(event.Parts) == 1 && event.Parts[0].Kind == tools.PartProviderNotice {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider notice was not emitted: %#v", emitted)
	}
}

func TestCollectProviderStreamPreservesTypedProviderError(t *testing.T) {
	events := make(chan protocol.StreamEvent, 1)
	events <- protocol.StreamEvent{
		Type: "error", Error: "request blocked",
		ProviderError: &protocol.ProviderErrorInfo{Code: "cyber_policy", Message: "request blocked", Retryable: false},
	}
	close(events)
	provider := &stalledStreamProvider{started: make(chan struct{}), events: events}
	_, err := collectProviderStream(context.Background(), provider, protocol.ChatRequest{}, nil)
	info, ok := protocol.ProviderErrorDetails(err)
	if !ok || info.Code != "cyber_policy" || info.Retryable {
		t.Fatalf("error = %v, info = %#v, ok = %v", err, info, ok)
	}
}

func TestProviderErrorNoticeKindDistinguishesPolicyAndTransportFailures(t *testing.T) {
	policy := providerErrorNoticePart(protocol.ProviderErrorInfo{Code: "cyber_policy", Message: "blocked"})
	if policy.NoticeKind != protocol.ProviderNoticePolicyError {
		t.Fatalf("policy notice kind = %q", policy.NoticeKind)
	}

	ordinary := providerErrorNoticePart(protocol.ProviderErrorInfo{
		Code: "upstream_timeout", Message: "gateway timeout", HTTPStatus: 504, Retryable: true,
	})
	if ordinary.NoticeKind != protocol.ProviderNoticeProviderError {
		t.Fatalf("ordinary provider notice kind = %q", ordinary.NoticeKind)
	}
}
