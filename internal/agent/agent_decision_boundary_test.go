package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type decisionBoundaryProvider struct {
	requests []protocol.ChatRequest
}

func (p *decisionBoundaryProvider) Name() string { return "decision-boundary" }

func (p *decisionBoundaryProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "decision-boundary-model"}}, nil
}

func (p *decisionBoundaryProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	return protocol.CompletionResult{}, errors.New("unexpected non-streaming completion")
}

func (p *decisionBoundaryProvider) Stream(_ context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.requests = append(p.requests, request)
	events := make(chan protocol.StreamEvent, 1)
	events <- protocol.StreamEvent{Type: "delta", Delta: "我会先根据任务目标选择需要的工具。"}
	close(events)
	return events, nil
}

func TestToolLoopTurnDoesNotRunSSHBeforeModelSelection(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()

	messages := []protocol.Message{
		{Role: "system", Content: "test system"},
		{Role: "user", Content: "帮我找回远程服务的管理密钥 echocode-credential://ssh-0000000000000000"},
	}
	service.sessionMessages = append([]protocol.Message(nil), messages...)
	provider := &decisionBoundaryProvider{}
	events := make([]ChatStreamEvent, 0, 2)

	result, err := service.runToolLoopTurn(
		context.Background(),
		provider,
		provider,
		protocol.ChatRequest{Model: "decision-boundary-model", Messages: messages},
		chatRoute{Provider: ModelProviderSetting{ID: "test", Name: "Test", Protocol: "openai-compatible"}, ModelID: "decision-boundary-model"},
		requestPrefixDiagnostic{},
		messages,
		len(messages),
		func(event ChatStreamEvent) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == "" || len(provider.requests) != 1 {
		t.Fatalf("result=%#v requests=%d", result, len(provider.requests))
	}
	for _, message := range provider.requests[0].Messages {
		if len(message.ToolCalls) != 0 || message.Name == "ssh" {
			t.Fatalf("host injected an SSH action before the model selected one: %#v", provider.requests[0].Messages)
		}
	}
	for _, event := range events {
		if event.ToolName == "ssh" {
			t.Fatalf("host emitted an implicit SSH event: %#v", events)
		}
	}
}

func TestServiceAllowsModelToBatchIndependentToolCalls(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	provider := &decisionBoundaryProvider{}
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "decision-boundary",
		SelectedModelID:    "decision-boundary-model",
		Providers: []ModelProviderSetting{{
			ID: "decision-boundary", Name: "Decision Boundary", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "decision-boundary-model",
			Models: []ProviderModel{{ID: "decision-boundary-model", Provider: "decision-boundary"}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }

	if _, err := service.SendChatMessage(context.Background(), "检查两个彼此独立的文件"); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 || !provider.requests[0].ParallelToolCalls {
		t.Fatalf("parallel tool request was not enabled: %#v", provider.requests)
	}
}
