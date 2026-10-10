package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

func TestFailoverScopesReasoningWithoutLosingToolHistory(t *testing.T) {
	first := chatRoute{Provider: ModelProviderSetting{ID: "first", BaseURL: "https://one.example/v1", Protocol: "openai-compatible"}, ModelID: "model", APIKey: "first-test-key"}
	second := chatRoute{Provider: ModelProviderSetting{ID: "second", BaseURL: "https://two.example/v1", Protocol: "openai-compatible"}, ModelID: "model", APIKey: "second-test-key"}
	continuation := scopedContinuation(first, &protocol.ProviderContinuation{Protocol: "openai-responses", Data: json.RawMessage(`[]`)})
	messages := []protocol.Message{{Role: "assistant", Content: "public text", ReasoningContent: "first route reasoning", Continuation: continuation, ToolCalls: []protocol.ToolCall{{ID: "call-1", Function: protocol.ToolCallFunction{Name: "read_file", Arguments: json.RawMessage(`{}`)}}}}, {Role: "tool", ToolCallID: "call-1", Content: "public tool result"}}
	forwarded := routeContinuationMessages(messages, second, 1)
	if forwarded[0].Continuation != nil || forwarded[0].ReasoningContent != "" || forwarded[0].Content != "public text" || forwarded[0].ToolCalls[0].ID != "call-1" || forwarded[1].ToolCallID != "call-1" {
		t.Fatalf("cross-route messages = %#v", forwarded)
	}
	if messages[0].Continuation == nil || messages[0].ReasoningContent == "" {
		t.Fatal("source history mutated")
	}
	forwarded[0].Continuation = scopedContinuation(second, &protocol.ProviderContinuation{Protocol: "openai-responses", Data: json.RawMessage(`[]`)})
	forwarded[0].ReasoningContent = "second route reasoning"
	next := routeContinuationMessages(forwarded, second, 1)
	if next[0].Continuation == nil || next[0].ReasoningContent != "second route reasoning" {
		t.Fatal("fallback route lost its own continuation")
	}
	rotated := second
	rotated.APIKey = "rotated-test-key"
	if routeContinuationMessages(forwarded, rotated, 1)[0].Continuation != nil {
		t.Fatal("credential change reused private continuation")
	}
}

func TestFailoverKeepsPartialCompletionOnFailure(t *testing.T) {
	provider := &failoverProvider{candidates: []routedProvider{{route: chatRoute{Provider: ModelProviderSetting{ID: "first"}}, provider: failoverProviderStub{complete: func(protocol.ChatRequest) (protocol.CompletionResult, error) {
		return protocol.CompletionResult{Content: "partial output", Reasoning: "summary"}, io.ErrUnexpectedEOF
	}}}}}
	result, err := provider.Complete(context.Background(), protocol.ChatRequest{})
	if err == nil || result.Content != "partial output" || result.Reasoning != "summary" {
		t.Fatalf("partial completion discarded: %#v, %v", result, err)
	}
}

func TestFailoverDoesNotMixReasoningAcrossProviders(t *testing.T) {
	first := make(chan protocol.StreamEvent, 2)
	first <- protocol.StreamEvent{Type: "reasoning", Delta: "first reasoning"}
	first <- protocol.StreamEvent{Type: "error", Error: "unexpected EOF"}
	close(first)
	called := false
	provider := &failoverProvider{candidates: []routedProvider{
		{route: chatRoute{Provider: ModelProviderSetting{ID: "first"}}, provider: failoverProviderStub{stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) { return first, nil }}},
		{route: chatRoute{Provider: ModelProviderSetting{ID: "second"}}, provider: failoverProviderStub{stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
			called = true
			return nil, errors.New("unexpected fallback")
		}}},
	}}
	events, err := provider.Stream(context.Background(), protocol.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	failed, reasoning := false, ""
	for event := range events {
		if event.Type == "error" {
			failed = true
		}
		if event.Type == "reasoning" {
			reasoning += event.Delta
		}
	}
	if called || !failed || reasoning != "first reasoning" {
		t.Fatal("failover mixed a partial reasoning stream")
	}
}

type failoverProviderStub struct {
	name     string
	complete func(protocol.ChatRequest) (protocol.CompletionResult, error)
	stream   func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error)
}

func (p failoverProviderStub) Name() string                                         { return p.name }
func (p failoverProviderStub) ListModels(context.Context) ([]protocol.Model, error) { return nil, nil }
func (p failoverProviderStub) Complete(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
	return p.complete(request)
}
func (p failoverProviderStub) Stream(_ context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	return p.stream(request)
}

func TestFailoverProviderRetriesCompletionOnAlternateRoute(t *testing.T) {
	provider := &failoverProvider{candidates: []routedProvider{
		{route: chatRoute{Provider: ModelProviderSetting{ID: "one", Name: "one"}, ModelID: "model-one"}, provider: failoverProviderStub{
			name: "one", complete: func(protocol.ChatRequest) (protocol.CompletionResult, error) {
				return protocol.CompletionResult{}, io.EOF
			},
		}},
		{route: chatRoute{
			Provider: ModelProviderSetting{ID: "two", Name: "two"}, ModelID: "model-two",
			Model: ProviderModel{ID: "model-two", MaxOutputTokens: 32_000, ReasoningLevels: []string{"none", "high"}, ThinkingModes: []string{"adaptive"}},
		}, provider: failoverProviderStub{
			name: "two", complete: func(request protocol.ChatRequest) (protocol.CompletionResult, error) {
				if request.Model != "model-two" {
					t.Fatalf("fallback model = %q", request.Model)
				}
				if request.MaxOutputTokens != 32_000 || len(request.ModelReasoningLevels) != 2 || len(request.ModelThinkingModes) != 1 {
					t.Fatalf("fallback model metadata = %#v", request)
				}
				return protocol.CompletionResult{Content: "ok"}, nil
			},
		}},
	}}
	result, err := provider.Complete(context.Background(), protocol.ChatRequest{Model: "ignored"})
	if err != nil || result.Content != "ok" || provider.ActiveRoute().Provider.ID != "two" {
		t.Fatalf("result=%#v route=%#v err=%v", result, provider.ActiveRoute(), err)
	}
}

func TestFailoverProviderRecomputesReasoningForEveryRoute(t *testing.T) {
	provider := &failoverProvider{candidates: []routedProvider{
		{
			route: chatRoute{
				Provider: ModelProviderSetting{ID: "xai", Name: "xAI", Protocol: "local", ReasoningProfile: "auto"},
				ModelID:  "grok-4.5",
			},
			provider: failoverProviderStub{name: "xai", complete: func(request protocol.ChatRequest) (protocol.CompletionResult, error) {
				if request.ReasoningEffort != "high" || request.ThinkingMode != "" {
					t.Fatalf("Grok reasoning = mode %q effort %q", request.ThinkingMode, request.ReasoningEffort)
				}
				return protocol.CompletionResult{}, io.EOF
			}},
		},
		{
			route: chatRoute{
				Provider: ModelProviderSetting{ID: "openai", Name: "OpenAI", Protocol: "local", ReasoningProfile: "auto"},
				ModelID:  "gpt-5.6-sol",
			},
			provider: failoverProviderStub{name: "openai", complete: func(request protocol.ChatRequest) (protocol.CompletionResult, error) {
				if request.ReasoningEffort != "max" || request.ThinkingMode != "" {
					t.Fatalf("fallback reasoning = mode %q effort %q", request.ThinkingMode, request.ReasoningEffort)
				}
				return protocol.CompletionResult{Content: "ok"}, nil
			}},
		},
	}}

	result, err := provider.Complete(context.Background(), protocol.ChatRequest{
		Model: "stale-model", ReasoningLevel: "max", ThinkingMode: "enabled", ReasoningEffort: "stale",
	})
	if err != nil || result.Content != "ok" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestFailoverProviderRetriesStreamBeforeVisibleOutput(t *testing.T) {
	errorStream := make(chan protocol.StreamEvent, 1)
	errorStream <- protocol.StreamEvent{Type: "error", Error: "unexpected EOF"}
	close(errorStream)
	okStream := make(chan protocol.StreamEvent, 2)
	okStream <- protocol.StreamEvent{Type: "delta", Delta: "ok"}
	okStream <- protocol.StreamEvent{Type: "done"}
	close(okStream)
	provider := &failoverProvider{candidates: []routedProvider{
		{route: chatRoute{Provider: ModelProviderSetting{ID: "one", Name: "one"}, ModelID: "one"}, provider: failoverProviderStub{name: "one", stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) { return errorStream, nil }}},
		{route: chatRoute{Provider: ModelProviderSetting{ID: "two", Name: "two"}, ModelID: "two"}, provider: failoverProviderStub{name: "two", stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) { return okStream, nil }}},
	}}
	events, err := provider.Stream(context.Background(), protocol.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var delta string
	for event := range events {
		if event.Type == "error" {
			t.Fatal(errors.New(event.Error))
		}
		delta += event.Delta
	}
	if delta != "ok" || provider.ActiveRoute().Provider.ID != "two" {
		t.Fatalf("delta=%q route=%#v", delta, provider.ActiveRoute())
	}
}

func TestFailoverProviderStopsWhileCandidateStreamIsOpen(t *testing.T) {
	stalled := make(chan protocol.StreamEvent)
	provider := &failoverProvider{candidates: []routedProvider{{
		route: chatRoute{Provider: ModelProviderSetting{ID: "one", Name: "one"}, ModelID: "one"},
		provider: failoverProviderStub{name: "one", stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
			return stalled, nil
		}},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Stream(ctx, protocol.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("cancelled failover stream emitted an unexpected event")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("failover stream did not close promptly after cancellation")
	}
	close(stalled)
}

func TestFailoverProviderDoesNotRetryTypedPolicyCompletionError(t *testing.T) {
	secondCalled := false
	provider := &failoverProvider{candidates: []routedProvider{
		{route: chatRoute{Provider: ModelProviderSetting{ID: "one", Name: "one"}, ModelID: "one"}, provider: failoverProviderStub{
			name: "one", complete: func(protocol.ChatRequest) (protocol.CompletionResult, error) {
				return protocol.CompletionResult{}, protocol.NewProviderError(protocol.ProviderErrorInfo{
					Code: "cyber_policy", Message: "temporarily unavailable by policy", Retryable: false,
				})
			},
		}},
		{route: chatRoute{Provider: ModelProviderSetting{ID: "two", Name: "two"}, ModelID: "two"}, provider: failoverProviderStub{
			name: "two", complete: func(protocol.ChatRequest) (protocol.CompletionResult, error) {
				secondCalled = true
				return protocol.CompletionResult{Content: "unexpected"}, nil
			},
		}},
	}}
	_, err := provider.Complete(context.Background(), protocol.ChatRequest{})
	info, ok := protocol.ProviderErrorDetails(err)
	if !ok || info.Code != "cyber_policy" || info.Retryable {
		t.Fatalf("error = %v, info = %#v, ok = %v", err, info, ok)
	}
	if secondCalled || provider.ActiveRoute().Provider.ID != "one" {
		t.Fatalf("policy error switched provider: called=%v route=%#v", secondCalled, provider.ActiveRoute())
	}
}

func TestFailoverProviderDoesNotRetryTypedPolicyStreamError(t *testing.T) {
	policyStream := make(chan protocol.StreamEvent, 1)
	policyStream <- protocol.StreamEvent{
		Type: "error", Error: "temporarily unavailable by policy",
		ProviderError: &protocol.ProviderErrorInfo{Code: "cyber_policy", Message: "temporarily unavailable by policy", Retryable: false},
	}
	close(policyStream)
	secondCalled := false
	provider := &failoverProvider{candidates: []routedProvider{
		{route: chatRoute{Provider: ModelProviderSetting{ID: "one", Name: "one"}, ModelID: "one"}, provider: failoverProviderStub{
			name: "one", stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) { return policyStream, nil },
		}},
		{route: chatRoute{Provider: ModelProviderSetting{ID: "two", Name: "two"}, ModelID: "two"}, provider: failoverProviderStub{
			name: "two", stream: func(protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
				secondCalled = true
				unexpected := make(chan protocol.StreamEvent)
				close(unexpected)
				return unexpected, nil
			},
		}},
	}}
	events, err := provider.Stream(context.Background(), protocol.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var received protocol.StreamEvent
	for event := range events {
		received = event
	}
	if received.ProviderError == nil || received.ProviderError.Code != "cyber_policy" {
		t.Fatalf("event = %#v", received)
	}
	if secondCalled || provider.ActiveRoute().Provider.ID != "one" {
		t.Fatalf("policy stream switched provider: called=%v route=%#v", secondCalled, provider.ActiveRoute())
	}
}
