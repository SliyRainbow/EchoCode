package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type routedProvider struct {
	route    chatRoute
	provider protocol.Provider
}

type failoverProvider struct {
	mu         sync.Mutex
	candidates []routedProvider
	active     int
	onSwitch   func(previous chatRoute, next chatRoute, cause error)
}

func (p *failoverProvider) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.candidates) == 0 {
		return "provider-failover"
	}
	return p.candidates[p.active].provider.Name()
}

func (p *failoverProvider) ListModels(ctx context.Context) ([]protocol.Model, error) {
	p.mu.Lock()
	candidate := p.candidates[p.active]
	p.mu.Unlock()
	return candidate.provider.ListModels(ctx)
}

func (p *failoverProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	candidate, index, events, err := p.openStream(ctx, request)
	if err != nil {
		return nil, err
	}
	output := make(chan protocol.StreamEvent, 16)
	go func() {
		defer close(output)
		currentCandidate, currentIndex, currentEvents := candidate, index, events
		for {
			seenOutput := false
			scopeAnnounced := false
			retry := false
		streamLoop:
			for {
				var event protocol.StreamEvent
				var ok bool
				select {
				case <-ctx.Done():
					go drainProviderStream(currentEvents)
					return
				case event, ok = <-currentEvents:
					if !ok {
						break streamLoop
					}
				}
				if event.Type == "error" && !seenOutput {
					streamErr := error(errors.New(event.Error))
					if event.ProviderError != nil {
						streamErr = protocol.NewProviderError(*event.ProviderError)
					}
					if isProviderFailoverError(ctx, streamErr) && p.advance(currentIndex, streamErr) {
						retry = true
						break streamLoop
					}
				}
				if event.Type == "delta" || event.Type == "reasoning" || event.Type == "tool_calls" || event.Type == "continuation" || event.Type == "usage" {
					seenOutput = true
				}
				if event.Type == "continuation" {
					event.Continuation = scopedContinuation(currentCandidate.route, event.Continuation)
					scopeAnnounced = true
				}
				if !scopeAnnounced && (event.Type == "reasoning" || event.Type == "tool_calls") {
					select {
					case <-ctx.Done():
						return
					case output <- protocol.StreamEvent{Type: "continuation", Continuation: scopedContinuation(currentCandidate.route, nil)}:
					}
					scopeAnnounced = true
				}
				select {
				case <-ctx.Done():
					return
				case output <- event:
				}
			}
			if !retry {
				return
			}
			var openErr error
			currentCandidate, currentIndex, currentEvents, openErr = p.openStream(ctx, request)
			if openErr != nil {
				select {
				case <-ctx.Done():
				case output <- protocol.StreamEvent{Type: "error", Error: openErr.Error()}:
				}
				return
			}
			_ = currentCandidate
		}
	}()
	return output, nil
}

func (p *failoverProvider) openStream(ctx context.Context, request protocol.ChatRequest) (routedProvider, int, <-chan protocol.StreamEvent, error) {
	var failures []error
	for {
		candidate, index, ok := p.currentCandidate()
		if !ok {
			return routedProvider{}, index, nil, errors.Join(failures...)
		}
		attempt := request
		attempt.Messages = routeContinuationMessages(request.Messages, candidate.route, index)
		applyRouteToChatRequest(&attempt, candidate.route)
		events, err := candidate.provider.Stream(ctx, attempt)
		if err == nil {
			return candidate, index, events, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", candidate.route.Provider.Name, err))
		if !isProviderFailoverError(ctx, err) || !p.advance(index, err) {
			return routedProvider{}, index, nil, errors.Join(failures...)
		}
	}
}

func (p *failoverProvider) Complete(ctx context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
	var failures []error
	for {
		candidate, index, ok := p.currentCandidate()
		if !ok {
			return protocol.CompletionResult{}, errors.Join(failures...)
		}
		caller, ok := candidate.provider.(protocol.ToolCaller)
		if !ok {
			err := fmt.Errorf("provider %s does not support tool completions", candidate.route.Provider.Name)
			failures = append(failures, err)
			if !p.advance(index, err) {
				return protocol.CompletionResult{}, errors.Join(failures...)
			}
			continue
		}
		attempt := request
		attempt.Messages = routeContinuationMessages(request.Messages, candidate.route, index)
		applyRouteToChatRequest(&attempt, candidate.route)
		completion, err := caller.Complete(ctx, attempt)
		if err == nil {
			if completion.Continuation != nil || completion.Reasoning != "" || len(completion.ToolCalls) > 0 {
				completion.Continuation = scopedContinuation(candidate.route, completion.Continuation)
			}
			return completion, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", candidate.route.Provider.Name, err))
		if completion.Content != "" || completion.Reasoning != "" || len(completion.ToolCalls) > 0 || completion.Usage != nil || !isProviderFailoverError(ctx, err) || !p.advance(index, err) {
			if completion.Continuation != nil || completion.Reasoning != "" || len(completion.ToolCalls) > 0 {
				completion.Continuation = scopedContinuation(candidate.route, completion.Continuation)
			}
			return completion, errors.Join(failures...)
		}
	}
}

func continuationScope(route chatRoute) string {
	// Bind to credentials without storing them in the checkpoint.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(route.Provider.ID+"\x00"+route.Provider.BaseURL+"\x00"+route.Provider.Protocol+"\x00"+route.Provider.APIType+"\x00"+route.ModelID+"\x00"+route.APIKey)))
}

func scopedContinuation(route chatRoute, continuation *protocol.ProviderContinuation) *protocol.ProviderContinuation {
	result := protocol.ProviderContinuation{}
	if continuation != nil {
		result = *continuation
	}
	result.Scope = continuationScope(route)
	return &result
}

func routeContinuationMessages(messages []protocol.Message, route chatRoute, candidateIndex int) []protocol.Message {
	result := append([]protocol.Message(nil), messages...)
	scope := continuationScope(route)
	for index := range result {
		continuation := result[index].Continuation
		if continuation != nil && continuation.Scope == scope {
			continue
		}
		if continuation != nil && continuation.Scope != "" || candidateIndex > 0 {
			result[index].Continuation = nil
			result[index].ReasoningContent = ""
		}
	}
	return result
}

func (p *failoverProvider) ActiveRoute() chatRoute {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.candidates) == 0 {
		return chatRoute{}
	}
	return p.candidates[p.active].route
}

func (p *failoverProvider) currentCandidate() (routedProvider, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active < 0 || p.active >= len(p.candidates) {
		return routedProvider{}, p.active, false
	}
	return p.candidates[p.active], p.active, true
}

func (p *failoverProvider) advance(index int, cause error) bool {
	p.mu.Lock()
	if index != p.active || index+1 >= len(p.candidates) {
		p.mu.Unlock()
		return false
	}
	previous := p.candidates[p.active].route
	p.active++
	next := p.candidates[p.active].route
	onSwitch := p.onSwitch
	p.mu.Unlock()
	if onSwitch != nil {
		onSwitch(previous, next, cause)
	}
	return true
}

func isProviderFailoverError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if info, ok := protocol.ProviderErrorDetails(err); ok {
		return info.Retryable
	}
	return isRetryablePostToolCompletionError(ctx, err)
}

func resolvedProviderRoute(fallback protocol.Provider, primary chatRoute) chatRoute {
	if provider, ok := fallback.(*failoverProvider); ok {
		if route := provider.ActiveRoute(); route.Provider.ID != "" {
			return route
		}
	}
	return primary
}
