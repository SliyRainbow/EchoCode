package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type emptyStreamProvider struct{}

type reasoningOnlyStreamProvider struct{}

func (emptyStreamProvider) Name() string { return "empty-stream" }

func (emptyStreamProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "empty-model"}}, nil
}

func (emptyStreamProvider) Stream(context.Context, protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	events := make(chan protocol.StreamEvent, 1)
	events <- protocol.StreamEvent{Type: "done"}
	close(events)
	return events, nil
}

func (reasoningOnlyStreamProvider) Name() string { return "reasoning-only" }

func (reasoningOnlyStreamProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "reasoning-only-model"}}, nil
}

func (reasoningOnlyStreamProvider) Stream(ctx context.Context, _ protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	events := make(chan protocol.StreamEvent, 1)
	go func() {
		defer close(events)
		select {
		case events <- protocol.StreamEvent{Type: "reasoning", Delta: "hidden analysis only"}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return events, nil
}

func TestEmptyStreamingResponseIsNotCommittedAsSuccess(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "empty-provider",
		SelectedModelID:    "empty-model",
		Providers: []ModelProviderSetting{{
			ID: "empty-provider", Name: "Empty", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "empty-model",
			Models: []ProviderModel{{ID: "empty-model", Provider: "empty-provider"}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return emptyStreamProvider{}, nil }

	result, err := service.SendChatMessage(context.Background(), "返回一条消息")
	if !errors.Is(err, errEmptyModelResponse) {
		t.Fatalf("empty response error = %v", err)
	}
	if result.TurnCommitted {
		t.Fatalf("empty response was committed: %#v", result)
	}
	provider, _, ok := findModelProvider(result.State.RuntimeSettings.Model.Providers, "empty-provider")
	if !ok || provider.LastSyncStatus != "error" {
		t.Fatalf("empty response provider status = %#v", provider)
	}
}

func TestReasoningOnlyCancellationRollsBackUserTurn(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "reasoning-only-provider",
		SelectedModelID:    "reasoning-only-model",
		Providers: []ModelProviderSetting{{
			ID: "reasoning-only-provider", Name: "Reasoning only", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "reasoning-only-model",
			Models: []ProviderModel{{ID: "reasoning-only-model", Provider: "reasoning-only-provider"}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return reasoningOnlyStreamProvider{}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := service.SendChatMessageWithEvents(ctx, "restore this reasoning-only draft", func(event ChatStreamEvent) {
		if event.Type == "reasoning" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reasoning-only cancellation error = %v", err)
	}
	if result.TurnCommitted {
		t.Fatalf("reasoning-only turn was committed: %#v", result)
	}
	if service.sessionState.TurnCount != 0 {
		t.Fatalf("turn count = %d after reasoning-only cancellation", service.sessionState.TurnCount)
	}
	for _, message := range service.GetSessionMessages() {
		if message.Content == "restore this reasoning-only draft" {
			t.Fatalf("reasoning-only user turn survived rollback: %#v", message)
		}
	}
}
