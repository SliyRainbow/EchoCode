package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type resumableLongTaskProvider struct {
	mode     string
	requests []protocol.ChatRequest
}

func (p *resumableLongTaskProvider) Name() string { return "resumable-long-task" }

func (p *resumableLongTaskProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "long-task-model"}}, nil
}

func (p *resumableLongTaskProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	return protocol.CompletionResult{}, errors.New("unexpected non-streaming completion")
}

func (p *resumableLongTaskProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.requests = append(p.requests, request)
	events := make(chan protocol.StreamEvent, 2)
	if p.mode == "resume" {
		events <- protocol.StreamEvent{Type: "delta", Delta: "已从中断位置继续，复用了原有产物。"}
		events <- protocol.StreamEvent{Type: "finish", FinishReason: "stop"}
		close(events)
		return events, nil
	}

	switch len(p.requests) {
	case 1:
		events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
			ID: "long-write-1", Type: "function",
			Function: protocol.ToolCallFunction{
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"long-result.txt","content":"checkpoint output\n"}`),
			},
		}}}
		close(events)
	case 2:
		go func() {
			defer close(events)
			events <- protocol.StreamEvent{Type: "delta", Delta: "已完成第一阶段，正在等待下一阶段。"}
			<-ctx.Done()
		}()
	default:
		close(events)
	}
	return events, nil
}

func TestInterruptedLongTaskResumesAfterServiceRestart(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	config := ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	}

	initialProvider := &resumableLongTaskProvider{mode: "initial"}
	service := newLongTaskResumeService(config, workspace, initialProvider)
	ctx, cancel := context.WithCancel(context.Background())
	result, err := service.SendChatMessageWithEvents(ctx, "生成长任务产物并继续验证", func(event ChatStreamEvent) {
		if event.Type == "delta" && strings.Contains(event.Delta, "第一阶段") {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("initial error = %v, want context.Canceled", err)
	}
	if !result.TurnCommitted || !strings.Contains(result.Content, "第一阶段") {
		t.Fatalf("interrupted turn was not retained: %#v", result)
	}
	artifactPath := filepath.Join(workspace, "long-result.txt")
	if data, readErr := os.ReadFile(artifactPath); readErr != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "checkpoint output\n" {
		t.Fatalf("checkpoint artifact data=%q err=%v", data, readErr)
	}
	terminalEvents := 0
	for _, event := range service.eventStore.Events() {
		if event.Type == eventlog.EventTurnTerminal && event.Payload.Status == "cancelled" {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("cancelled terminal events = %d, want 1", terminalEvents)
	}
	service.Close()

	resumeProvider := &resumableLongTaskProvider{mode: "resume"}
	restarted := newLongTaskResumeService(config, workspace, resumeProvider)
	defer restarted.Close()
	resumed, err := restarted.SendChatMessage(context.Background(), "继续")
	if err != nil || !strings.Contains(resumed.Content, "复用了原有产物") {
		t.Fatalf("resumed result=%#v err=%v", resumed, err)
	}
	if len(resumeProvider.requests) != 1 {
		t.Fatalf("resume provider calls = %d, want 1", len(resumeProvider.requests))
	}
	privateContext := latestPrivateTurnContext(resumeProvider.requests[0].Messages)
	for _, expected := range []string{"[execution_state]", "write_file", "long-result.txt"} {
		if !strings.Contains(privateContext, expected) {
			t.Fatalf("resume context is missing %q: %q", expected, privateContext)
		}
	}
	if !protocolMessagesContain(resumeProvider.requests[0].Messages, "第一阶段") {
		t.Fatalf("resume request lost the interrupted assistant progress: %#v", resumeProvider.requests[0].Messages)
	}
	if data, readErr := os.ReadFile(artifactPath); readErr != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "checkpoint output\n" {
		t.Fatalf("resume repeated or lost the checkpoint artifact: data=%q err=%v", data, readErr)
	}
}

func newLongTaskResumeService(config ServiceConfig, workspace string, provider protocol.Provider) *Service {
	service := NewService(config)
	service.reasoning = ReasoningHigh
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.SandboxMode = "workspace-write"
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.runtimeSettings.Team.Enabled = false
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "long-task-local",
		SelectedModelID:    "long-task-model",
		Providers: []ModelProviderSetting{{
			ID: "long-task-local", Name: "Long Task Local", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "long-task-model",
			Models: []ProviderModel{{ID: "long-task-model", DisplayName: "Long task model", Provider: "long-task-local", ContextWindowTokens: 128000}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
	return service
}

func latestPrivateTurnContext(messages []protocol.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].InternalKind == contextRequestKind {
			return messages[index].Content
		}
	}
	return ""
}

func protocolMessagesContain(messages []protocol.Message, expected string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, expected) {
			return true
		}
	}
	return false
}
