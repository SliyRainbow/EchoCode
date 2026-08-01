package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type subagentProbeProvider struct {
	mu          sync.Mutex
	requests    []protocol.ChatRequest
	active      int
	maxActive   int
	delay       time.Duration
	block       bool
	started     chan struct{}
	startedOnce sync.Once
}

type subagentEndToEndProvider struct {
	mu                       sync.Mutex
	mainCalls                int
	childCalls               int
	childTools               []protocol.ToolDefinition
	sawDelegatedResult       bool
	sawPendingSubagentPrompt bool
}

type parentChildParallelProvider struct {
	mu                       sync.Mutex
	mainCalls                int
	childCalls               int
	sawChildResult           bool
	sawPendingSubagentPrompt bool
	childStarted             chan struct{}
	mainProgressed           chan struct{}
	releaseChild             chan struct{}
	childOnce                sync.Once
	mainOnce                 sync.Once
}

type cancellingImplementProvider struct {
	mu               sync.Mutex
	mainCalls        int
	childCalls       int
	childWriteDone   chan struct{}
	childWriteSignal sync.Once
}

type selectiveSubagentProvider struct {
	started   chan string
	cancelled chan string
	release   chan struct{}
}

func (p *selectiveSubagentProvider) Name() string { return "selective-subagent" }

func (p *selectiveSubagentProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "subagent-model"}}, nil
}

func (p *selectiveSubagentProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	taskID := request.Metadata["subagent_task_id"]
	events := make(chan protocol.StreamEvent, 2)
	go func() {
		defer close(events)
		p.started <- taskID
		select {
		case <-ctx.Done():
			p.cancelled <- taskID
		case <-p.release:
			events <- protocol.StreamEvent{Type: "delta", Delta: "worker completed"}
		}
	}()
	return events, nil
}

func (p *subagentProbeProvider) Name() string { return "subagent-probe" }

func (p *subagentProbeProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "subagent-model"}}, nil
}

func (p *subagentProbeProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()
	if p.started != nil {
		p.startedOnce.Do(func() { close(p.started) })
	}

	events := make(chan protocol.StreamEvent, 2)
	go func() {
		defer close(events)
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		if p.block {
			<-ctx.Done()
			return
		}
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			events <- protocol.StreamEvent{Type: "delta", Delta: "已完成独立检查。"}
			events <- protocol.StreamEvent{Type: "usage", Usage: &protocol.TokenUsage{PromptTokens: 20, CompletionTokens: 5}}
		}
	}()
	return events, nil
}

func (p *subagentProbeProvider) snapshot() ([]protocol.ChatRequest, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.ChatRequest(nil), p.requests...), p.maxActive
}

func (p *subagentEndToEndProvider) Name() string { return "subagent-e2e" }

func (p *subagentEndToEndProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "subagent-model"}}, nil
}

func (p *subagentEndToEndProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	return protocol.CompletionResult{}, errors.New("unexpected non-streaming completion")
}

func (p *subagentEndToEndProvider) Stream(_ context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	events := make(chan protocol.StreamEvent, 2)
	if request.Metadata["request_kind"] == "subagent" {
		p.mu.Lock()
		p.childCalls++
		p.childTools = append([]protocol.ToolDefinition(nil), request.Tools...)
		p.mu.Unlock()
		events <- protocol.StreamEvent{Type: "delta", Delta: "子代理确认了真实目录结构。"}
		close(events)
		return events, nil
	}

	p.mu.Lock()
	p.mainCalls++
	mainCall := p.mainCalls
	for _, message := range request.Messages {
		if message.InternalKind == "subagent-pending" {
			p.sawPendingSubagentPrompt = true
			break
		}
	}
	if len(request.Messages) > 0 && request.Messages[len(request.Messages)-1].Role == "tool" {
		p.sawDelegatedResult = strings.Contains(request.Messages[len(request.Messages)-1].Content, "子代理确认了真实目录结构")
	}
	p.mu.Unlock()
	if mainCall == 1 {
		events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
			ID: "delegate-1", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "delegate_task",
				Arguments: json.RawMessage(`{"tasks":[{"label":"结构检查","task":"检查当前项目结构并返回证据","agentType":"explore"}]}`),
			},
		}}}
	} else {
		events <- protocol.StreamEvent{Type: "delta", Delta: "主 Agent 已综合子代理结果。"}
	}
	close(events)
	return events, nil
}

func (p *parentChildParallelProvider) Name() string { return "parent-child-parallel" }

func (p *parentChildParallelProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "subagent-model"}}, nil
}

func (p *parentChildParallelProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	return protocol.CompletionResult{}, errors.New("unexpected non-streaming completion")
}

func (p *parentChildParallelProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	events := make(chan protocol.StreamEvent, 2)
	if request.Metadata["request_kind"] == "subagent" {
		p.mu.Lock()
		p.childCalls++
		p.mu.Unlock()
		p.childOnce.Do(func() { close(p.childStarted) })
		go func() {
			defer close(events)
			select {
			case <-ctx.Done():
				return
			case <-p.releaseChild:
				events <- protocol.StreamEvent{Type: "delta", Delta: "后台审阅完成。"}
			}
		}()
		return events, nil
	}

	p.mu.Lock()
	p.mainCalls++
	mainCall := p.mainCalls
	for _, message := range request.Messages {
		if message.InternalKind == "subagent-pending" {
			p.sawPendingSubagentPrompt = true
			break
		}
	}
	if len(request.Messages) > 0 && request.Messages[len(request.Messages)-1].Role == "tool" {
		p.sawChildResult = p.sawChildResult || strings.Contains(request.Messages[len(request.Messages)-1].Content, "后台审阅完成")
	}
	p.mu.Unlock()
	switch mainCall {
	case 1:
		events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
			ID: "delegate-parallel", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "delegate_task",
				Arguments: json.RawMessage(`{"tasks":[{"label":"后台审阅","task":"等待主 Agent 完成独立检查后再返回","agentType":"review"}]}`),
			},
		}}}
	case 2:
		events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
			ID: "main-list", Type: "function", Function: protocol.ToolCallFunction{
				Name: "list_dir", Arguments: json.RawMessage(`{"path":"."}`),
			},
		}}}
	case 3:
		p.mainOnce.Do(func() { close(p.mainProgressed) })
		events <- protocol.StreamEvent{Type: "delta", Delta: "主 Agent 已完成自己的目录检查。"}
	default:
		events <- protocol.StreamEvent{Type: "delta", Delta: "主 Agent 已并行完成并综合子代理结果。"}
	}
	close(events)
	return events, nil
}

func (p *cancellingImplementProvider) Name() string { return "cancelling-implement" }

func (p *cancellingImplementProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "subagent-model"}}, nil
}

func (p *cancellingImplementProvider) Complete(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error) {
	return protocol.CompletionResult{}, errors.New("unexpected non-streaming completion")
}

func (p *cancellingImplementProvider) Stream(ctx context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	events := make(chan protocol.StreamEvent, 1)
	if request.Metadata["request_kind"] == "subagent" {
		p.mu.Lock()
		p.childCalls++
		childCall := p.childCalls
		p.mu.Unlock()
		if childCall == 1 {
			events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
				ID: "child-write", Type: "function", Function: protocol.ToolCallFunction{
					Name: "write_file", Arguments: json.RawMessage(`{"path":"child.txt","content":"after\n"}`),
				},
			}}}
			close(events)
			return events, nil
		}
		p.childWriteSignal.Do(func() { close(p.childWriteDone) })
		go func() {
			defer close(events)
			<-ctx.Done()
		}()
		return events, nil
	}

	p.mu.Lock()
	p.mainCalls++
	mainCall := p.mainCalls
	p.mu.Unlock()
	if mainCall == 1 {
		events <- protocol.StreamEvent{Type: "tool_calls", ToolCalls: []protocol.ToolCall{{
			ID: "delegate-implement", Type: "function", Function: protocol.ToolCallFunction{
				Name:      "delegate_task",
				Arguments: json.RawMessage(`{"tasks":[{"label":"写入后等待","task":"创建 child.txt 后等待","agentType":"implement"}]}`),
			},
		}}}
	} else {
		events <- protocol.StreamEvent{Type: "delta", Delta: "主 Agent 等待后台实现完成。"}
	}
	close(events)
	return events, nil
}

func newSubagentToolTest(t *testing.T, provider protocol.Provider) (*Service, context.Context) {
	t.Helper()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
	route := chatRoute{
		Provider: ModelProviderSetting{ID: "probe", Name: "Probe", Protocol: "local", Enabled: true},
		ModelID:  "subagent-model",
	}
	scope := subagentExecutionScope{
		BaseRequest: protocol.ChatRequest{
			Model:      route.ModelID,
			Messages:   []protocol.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "inspect the project"}},
			Metadata:   map[string]string{"task_kind": "chat"},
			SessionID:  "session-test",
			ThreadID:   "thread-test",
			TurnID:     "turn-test",
			ToolChoice: "auto",
		},
		PrimaryRoute: route,
	}
	return service, withSubagentExecutionScope(context.Background(), scope)
}

func TestDelegateTaskRunsReadOnlyWorkersConcurrentlyWithoutCoordinatorTools(t *testing.T) {
	provider := &subagentProbeProvider{delay: 200 * time.Millisecond}
	service, ctx := newSubagentToolTest(t, provider)
	args := json.RawMessage(`{"tasks":[
		{"label":"结构探索","task":"检查目录结构","agentType":"explore"},
		{"label":"风险审阅","task":"检查潜在回归","agentType":"review"}
	]}`)
	startedAt := time.Now()
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("delegate result = %#v", result)
	}
	if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
		t.Fatalf("delegate_task blocked for %s instead of returning immediately", elapsed)
	}
	assertSubagentParts(t, result.Parts, 2, "pending")
	collected, err := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"wait":true}`))
	if err != nil {
		t.Fatal(err)
	}
	requests, maxActive := provider.snapshot()
	if maxActive < 2 {
		t.Fatalf("read-only workers did not overlap, max active = %d", maxActive)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for _, request := range requests {
		if request.Metadata["request_kind"] != "subagent" || request.Metadata["subagent_task_id"] == "" {
			t.Fatalf("missing subagent metadata: %#v", request.Metadata)
		}
		assertSubagentToolSet(t, request.Tools, false)
	}
	assertSubagentParts(t, collected.Parts, 2, "completed")
	service.finishSubagentTurn(false)
}

func TestAwaitSubagentsDefaultsToNonBlocking(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	_, control := service.registerSubagent(context.Background(), tools.ResultPart{
		Kind: tools.PartSubagent, TaskID: "nonblocking-child", AgentType: subagentExplore,
		Label: "background", Status: "running",
	})

	started := time.Now()
	result, err := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || result.IsError {
		t.Fatalf("poll result=%#v err=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("default await blocked for %s", elapsed)
	}
	assertSubagentParts(t, result.Parts, 1, "running")
	control.finish(delegatedTaskResult{part: tools.ResultPart{
		Kind: tools.PartSubagent, TaskID: "nonblocking-child", AgentType: subagentExplore,
		Label: "background", Status: "cancelled",
	}})
}

func TestMainAgentDelegatesAndSynthesizesSubagentResult(t *testing.T) {
	provider := &subagentEndToEndProvider{}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.reasoning = ReasoningHigh
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.SandboxMode = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "probe",
		SelectedModelID:    "subagent-model",
		Providers: []ModelProviderSetting{{
			ID: "probe", Name: "Probe", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "subagent-model",
			Models: []ProviderModel{{ID: "subagent-model", DisplayName: "Subagent", Provider: "probe", ContextWindowTokens: 128000}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
	var liveStatuses []string
	var liveOutput string
	result, err := service.SendChatMessageWithEvents(context.Background(), "请并行检查项目结构", func(event ChatStreamEvent) {
		for _, part := range event.Parts {
			if part.Kind == tools.PartSubagent {
				liveStatuses = append(liveStatuses, part.Status)
				if part.SubagentOutput != "" {
					liveOutput = part.SubagentOutput
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "主 Agent 已综合子代理结果。" {
		t.Fatalf("result content = %q", result.Content)
	}
	provider.mu.Lock()
	mainCalls, childCalls := provider.mainCalls, provider.childCalls
	childTools := append([]protocol.ToolDefinition(nil), provider.childTools...)
	sawDelegatedResult := provider.sawDelegatedResult
	sawPendingSubagentPrompt := provider.sawPendingSubagentPrompt
	provider.mu.Unlock()
	if mainCalls != 4 || childCalls != 1 || !sawDelegatedResult || !sawPendingSubagentPrompt {
		t.Fatalf("provider calls main=%d child=%d saw result=%v saw pending reminder=%v", mainCalls, childCalls, sawDelegatedResult, sawPendingSubagentPrompt)
	}
	assertSubagentToolSet(t, childTools, false)
	assertSubagentParts(t, result.Parts, 1, "completed")
	if !containsString(liveStatuses, "running") || !containsString(liveStatuses, "completed") {
		t.Fatalf("live subagent statuses = %#v", liveStatuses)
	}
	if !strings.Contains(liveOutput, "子代理确认了真实目录结构") {
		t.Fatalf("live subagent output = %q", liveOutput)
	}
}

func TestMainAgentContinuesToolWorkWhileSubagentRuns(t *testing.T) {
	provider := &parentChildParallelProvider{
		childStarted:   make(chan struct{}),
		mainProgressed: make(chan struct{}),
		releaseChild:   make(chan struct{}),
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.reasoning = ReasoningHigh
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.SandboxMode = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "probe",
		SelectedModelID:    "subagent-model",
		Providers: []ModelProviderSetting{{
			ID: "probe", Name: "Probe", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "subagent-model",
			Models: []ProviderModel{{ID: "subagent-model", DisplayName: "Subagent", Provider: "probe", ContextWindowTokens: 128000}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }

	type chatOutcome struct {
		result ChatResult
		err    error
	}
	var eventsMu sync.Mutex
	var events []ChatStreamEvent
	done := make(chan chatOutcome, 1)
	go func() {
		result, err := service.SendChatMessageWithEvents(context.Background(), "并行检查项目", func(event ChatStreamEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		})
		done <- chatOutcome{result: result, err: err}
	}()

	select {
	case <-provider.childStarted:
	case <-time.After(time.Second):
		t.Fatal("subagent did not start")
	}
	select {
	case <-provider.mainProgressed:
	case <-time.After(time.Second):
		t.Fatal("main Agent did not continue its own tool work while child was blocked")
	}
	close(provider.releaseChild)

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Content != "主 Agent 已并行完成并综合子代理结果。" {
			t.Fatalf("result content = %q", outcome.result.Content)
		}
		assertSubagentParts(t, outcome.result.Parts, 1, "completed")
	case <-time.After(2 * time.Second):
		t.Fatal("parallel parent/child turn did not complete")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.mainCalls != 5 || provider.childCalls != 1 || !provider.sawChildResult || !provider.sawPendingSubagentPrompt {
		t.Fatalf("provider calls main=%d child=%d saw child=%v saw pending reminder=%v", provider.mainCalls, provider.childCalls, provider.sawChildResult, provider.sawPendingSubagentPrompt)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	delegateCompleted := false
	sawSubagentEvent := false
	for _, event := range events {
		if event.Type == "tool" && event.ToolName == "delegate_task" && event.Status == "completed" {
			delegateCompleted = true
			continue
		}
		if event.Type == "tool" && event.ToolName == "delegate_task" && event.Status == "running" && delegateCompleted {
			t.Fatalf("delegate_task regressed to running after completion: %#v", event)
		}
		if event.Type == "subagent" && len(event.Parts) == 1 && event.Parts[0].Kind == tools.PartSubagent {
			sawSubagentEvent = true
		}
	}
	if !delegateCompleted || !sawSubagentEvent {
		t.Fatalf("delegate completed=%v subagent event=%v events=%#v", delegateCompleted, sawSubagentEvent, events)
	}
}

func TestParentCancellationJoinsImplementWorkerBeforeRollback(t *testing.T) {
	workspace := t.TempDir()
	provider := &cancellingImplementProvider{childWriteDone: make(chan struct{})}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	service.reasoning = ReasoningHigh
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.SandboxMode = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "probe",
		SelectedModelID:    "subagent-model",
		Providers: []ModelProviderSetting{{
			ID: "probe", Name: "Probe", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: "subagent-model",
			Models: []ProviderModel{{ID: "subagent-model", DisplayName: "Subagent", Provider: "probe", ContextWindowTokens: 128000}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
	turn := service.captureTurnSnapshot()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.SendChatMessageWithEvents(ctx, "让后台实现后等待", nil)
		done <- err
	}()
	select {
	case <-provider.childWriteDone:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("implement subagent did not finish its write")
	}
	path := filepath.Join(workspace, "child.txt")
	if content, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(content)) != "after" {
		cancel()
		t.Fatalf("child write was not visible before cancellation: content=%q err=%v", content, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parent result error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled parent did not join its implement subagent")
	}
	if err := service.rollbackTurn(turn); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("child write survived parent rollback: %v", err)
	}
}

func TestDelegateTaskRunsImplementWorkersConcurrentlyAndAllowsWriteTools(t *testing.T) {
	provider := &subagentProbeProvider{delay: 45 * time.Millisecond}
	service, ctx := newSubagentToolTest(t, provider)
	args := json.RawMessage(`{"tasks":[
		{"label":"实现一","task":"完成第一项修改","agentType":"implement"},
		{"label":"实现二","task":"完成第二项修改","agentType":"implement"}
	]}`)
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, result.Parts, 2, "pending")
	collected, err := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"wait":true}`))
	if err != nil {
		t.Fatal(err)
	}
	requests, maxActive := provider.snapshot()
	if maxActive < 2 {
		t.Fatalf("implement workers did not overlap, max active = %d", maxActive)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for _, request := range requests {
		assertSubagentToolSet(t, request.Tools, true)
	}
	assertSubagentParts(t, collected.Parts, 2, "completed")
	service.finishSubagentTurn(false)
}

func TestDelegateTaskRejectsFanoutBeyondActiveWorkerLimit(t *testing.T) {
	provider := &subagentProbeProvider{block: true}
	service, ctx := newSubagentToolTest(t, provider)
	service.runtimeSettings.MaxConcurrentSubagents = 2
	first, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[
		{"label":"first","task":"wait","agentType":"explore"},
		{"label":"second","task":"wait","agentType":"review"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, first.Parts, 2, "pending")

	second, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[
		{"label":"third","task":"wait","agentType":"explore"},
		{"label":"fourth","task":"wait","agentType":"review"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !second.IsError || !strings.Contains(second.Summary, "并行子代理上限为 2") {
		t.Fatalf("fanout result = %#v", second)
	}
	if active := service.activeSubagentCount(); active != 2 {
		t.Fatalf("active subagents = %d, want 2", active)
	}
	service.finishSubagentTurn(true)
}

func TestDelegateTaskSchemaUsesConfiguredConcurrencyLimit(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.MaxConcurrentSubagents = 6
	schema := (DelegateTaskTool{Service: service}).InputSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema)
	}
	tasks, ok := properties["tasks"].(map[string]any)
	if !ok || tasks["maxItems"] != 6 {
		t.Fatalf("tasks schema = %#v", tasks)
	}
	if _, err := normalizeDelegatedTaskSpecs(make([]delegateTaskSpec, 7), 6); err == nil {
		t.Fatal("delegated task specs exceeded configured limit without an error")
	}
}

func TestReadOnlyDelegateTaskOmitsAndRejectsImplementWorkers(t *testing.T) {
	provider := &subagentProbeProvider{delay: time.Millisecond}
	service, ctx := newSubagentToolTest(t, provider)
	tool := DelegateTaskTool{Service: service, ReadOnly: true}

	schema := tool.InputSchema()
	properties := schema["properties"].(map[string]any)
	tasks := properties["tasks"].(map[string]any)
	items := tasks["items"].(map[string]any)
	itemProperties := items["properties"].(map[string]any)
	agentType := itemProperties["agentType"].(map[string]any)
	agentTypes := agentType["enum"].([]string)
	if containsString(agentTypes, subagentImplement) {
		t.Fatalf("read-only delegate schema advertised implement: %#v", agentTypes)
	}
	if !containsString(agentTypes, subagentExplore) || !containsString(agentTypes, subagentReview) {
		t.Fatalf("read-only delegate schema omitted read-only workers: %#v", agentTypes)
	}

	result, err := tool.Execute(ctx, json.RawMessage(`{"tasks":[{"label":"错误实现","task":"修改文件","agentType":"implement"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Summary, "当前工作区是只读权限") {
		t.Fatalf("read-only implement result = %#v", result)
	}
	if active := service.activeSubagentCount(); active != 0 {
		t.Fatalf("read-only rejection started %d workers", active)
	}
}

func TestSubagentOverflowUsesVisibleMarkers(t *testing.T) {
	part := tools.ResultPart{Kind: tools.PartSubagent}
	indexes := make(map[string]int)
	for index := 0; index < maxSubagentActivities+5; index++ {
		upsertSubagentActivity(&part, indexes, ChatStreamEvent{
			Type: "tool", ToolName: "read_file", ToolCallID: fmt.Sprintf("call-%d", index), Status: "completed",
		})
	}
	if len(part.Activities) != maxSubagentActivities {
		t.Fatalf("activity count = %d, want %d", len(part.Activities), maxSubagentActivities)
	}
	lastActivity := part.Activities[len(part.Activities)-1]
	if lastActivity.ID != subagentActivityOverflowID || lastActivity.Output != subagentActivityOverflow {
		t.Fatalf("activity overflow marker = %#v", lastActivity)
	}

	part.Steps = make([]tools.ProgressStep, maxSubagentTimelineSteps)
	for index := range part.Steps {
		part.Steps[index] = tools.ProgressStep{Title: fmt.Sprintf("step-%d", index), Status: "completed"}
	}
	markSubagentStepOverflow(&part)
	if len(part.Steps) != maxSubagentTimelineSteps || part.Steps[len(part.Steps)-1].Title != subagentStepOverflow {
		t.Fatalf("step overflow marker = %#v", part.Steps)
	}
}

func TestCompletedSubagentsReleaseActiveWorkerSlotsBeforeCollection(t *testing.T) {
	provider := &subagentProbeProvider{delay: time.Millisecond}
	service, ctx := newSubagentToolTest(t, provider)
	first, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[
		{"label":"first","task":"finish","agentType":"explore"},
		{"label":"second","task":"finish","agentType":"review"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, first.Parts, 2, "pending")
	deadline := time.Now().Add(time.Second)
	for service.activeSubagentCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active := service.activeSubagentCount(); active != 0 {
		t.Fatalf("completed subagents still occupy %d active slots", active)
	}

	second, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[
		{"label":"third","task":"finish","agentType":"explore"},
		{"label":"fourth","task":"finish","agentType":"review"},
		{"label":"fifth","task":"finish","agentType":"explore"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if second.IsError {
		t.Fatalf("released slots rejected new workers: %#v", second)
	}
	assertSubagentParts(t, second.Parts, 3, "pending")
	service.finishSubagentTurn(false)
}

func TestDelegateTaskCancellationPersistsCancelledWorkerState(t *testing.T) {
	provider := &subagentProbeProvider{block: true, started: make(chan struct{})}
	service, baseCtx := newSubagentToolTest(t, provider)
	ctx, cancel := context.WithCancel(baseCtx)
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[{"label":"阻塞检查","task":"等待取消","agentType":"explore"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, result.Parts, 1, "pending")

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("subagent provider did not start")
	}
	cancel()
	done := make(chan []tools.ResultPart, 1)
	go func() { done <- service.finishSubagentTurn(true) }()
	select {
	case parts := <-done:
		assertSubagentParts(t, parts, 1, "cancelled")
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not join the delegated task promptly")
	}
}

func TestFinishSubagentTurnCancelsWorkersWhenParentContextEnds(t *testing.T) {
	provider := &subagentProbeProvider{block: true, started: make(chan struct{})}
	service, ctx := newSubagentToolTest(t, provider)
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[{"label":"阻塞检查","task":"等待父任务结束","agentType":"explore"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, result.Parts, 1, "pending")
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("subagent provider did not start")
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan []tools.ResultPart, 1)
	go func() { done <- service.finishSubagentTurnWithContext(parentCtx, false) }()
	select {
	case parts := <-done:
		assertSubagentParts(t, parts, 1, "cancelled")
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not cancel and join the delegated task promptly")
	}
}

func TestFinishSubagentTurnDetachesWorkerThatNeverAcknowledgesCancellation(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	_, control := service.registerSubagent(context.Background(), tools.ResultPart{
		Kind:      tools.PartSubagent,
		TaskID:    "stuck-worker",
		AgentType: subagentExplore,
		Label:     "无响应上游",
		Status:    "running",
		Steps:     []tools.ProgressStep{{Title: "等待上游", Status: "in_progress"}},
	})

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	parts := service.finishSubagentTurnWithContext(parent, false)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation barrier blocked for %s", elapsed)
	}
	if len(parts) != 1 || parts[0].TaskID != "stuck-worker" || parts[0].Status != "cancelled" || !strings.Contains(parts[0].Summary, "后台清理") {
		t.Fatalf("detached worker result = %#v", parts)
	}
	if active := service.activeSubagentCount(); active != 0 {
		t.Fatalf("detached worker remained registered: %d", active)
	}
	// A late completion is intentionally ignored by the completed parent turn.
	control.finish(delegatedTaskResult{part: tools.ResultPart{Kind: tools.PartSubagent, TaskID: "stuck-worker", Status: "completed"}})
}

func TestCancelSubagentStopsOnlySelectedWorker(t *testing.T) {
	provider := &selectiveSubagentProvider{
		started: make(chan string, 2), cancelled: make(chan string, 1), release: make(chan struct{}),
	}
	service, baseCtx := newSubagentToolTest(t, provider)
	var mu sync.Mutex
	var taskIDs []string
	ctx := tools.WithProgressSink(baseCtx, func(part tools.ResultPart) {
		if part.Kind != tools.PartSubagent || part.Status != "pending" {
			return
		}
		mu.Lock()
		taskIDs = append(taskIDs, part.TaskID)
		mu.Unlock()
	})
	result, err := (DelegateTaskTool{Service: service}).Execute(ctx, json.RawMessage(`{"tasks":[
		{"label":"first","task":"wait","agentType":"explore"},
		{"label":"second","task":"wait","agentType":"review"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	assertSubagentParts(t, result.Parts, 2, "pending")

	started := map[string]bool{}
	for len(started) < 2 {
		select {
		case taskID := <-provider.started:
			started[taskID] = true
		case <-time.After(time.Second):
			t.Fatal("workers did not start concurrently")
		}
	}
	mu.Lock()
	if len(taskIDs) != 2 {
		mu.Unlock()
		t.Fatalf("pending task IDs = %#v", taskIDs)
	}
	selected := taskIDs[0]
	mu.Unlock()
	if !service.CancelSubagent(selected) {
		t.Fatal("selected subagent was not cancelled")
	}
	select {
	case cancelled := <-provider.cancelled:
		if cancelled != selected {
			t.Fatalf("cancelled worker = %q, want %q", cancelled, selected)
		}
	case <-time.After(time.Second):
		t.Fatal("selected worker did not stop promptly")
	}
	close(provider.release)

	done := make(chan tools.Result, 1)
	go func() {
		collected, _ := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"wait":true}`))
		done <- collected
	}()
	select {
	case result := <-done:
		statuses := map[string]string{}
		for _, part := range result.Parts {
			if part.Kind == tools.PartSubagent {
				statuses[part.TaskID] = part.Status
			}
		}
		if statuses[selected] != "cancelled" {
			t.Fatalf("selected status = %q", statuses[selected])
		}
		completed := 0
		for taskID, status := range statuses {
			if taskID != selected && status == "completed" {
				completed++
			}
		}
		if completed != 1 {
			t.Fatalf("sibling status map = %#v", statuses)
		}
	case <-time.After(time.Second):
		t.Fatal("await_subagents did not finish after sibling completed")
	}
	service.finishSubagentTurn(false)
}

func TestSubagentPartRoundTripsThroughEventLog(t *testing.T) {
	source := []tools.ResultPart{{
		Kind: tools.PartSubagent, TaskID: "subagent-1", AgentType: "review", Label: "审阅",
		Status: "completed", ProviderID: "provider", Model: "model", Summary: "没有发现回归。",
		CurrentAction: "已完成", Steps: []tools.ProgressStep{{Title: "检查测试", Status: "completed"}},
		SubagentOutput: "审阅输出", SubagentReasoning: "检查路径",
		Activities:   []tools.SubagentActivity{{ID: "tool-1", Kind: "tool", Title: "读取文件", Status: "completed", Output: "ok"}},
		ChangedFiles: 2, Additions: 4, Deletions: 1, DurationMs: 25,
	}}
	restored := fromEventParts(toEventParts(source))
	if len(restored) != 1 {
		t.Fatalf("restored parts = %#v", restored)
	}
	part := restored[0]
	if part.Kind != tools.PartSubagent || part.TaskID != source[0].TaskID || part.AgentType != "review" || part.CurrentAction != "已完成" {
		t.Fatalf("restored subagent part = %#v", part)
	}
	if len(part.Steps) != 1 || part.Steps[0].Title != "检查测试" || part.ChangedFiles != 2 {
		t.Fatalf("restored subagent details = %#v", part)
	}
	if part.SubagentOutput != "审阅输出" || part.SubagentReasoning != "检查路径" || len(part.Activities) != 1 || part.Activities[0].Output != "ok" {
		t.Fatalf("restored subagent transcript = %#v", part)
	}
}

func TestMergeOutcomePartsUpsertsSubagentWithoutTerminalRegression(t *testing.T) {
	pending := tools.ResultPart{Kind: tools.PartSubagent, TaskID: "subagent-1", Status: "pending", Label: "审阅"}
	completed := tools.ResultPart{Kind: tools.PartSubagent, TaskID: "subagent-1", Status: "completed", Label: "审阅", Summary: "完成"}
	parts := mergeOutcomeParts([]tools.ResultPart{pending}, []tools.ResultPart{completed})
	assertSubagentParts(t, parts, 1, "completed")

	parts = mergeOutcomeParts(parts, []tools.ResultPart{pending})
	assertSubagentParts(t, parts, 1, "completed")
	if parts[0].Summary != "完成" {
		t.Fatalf("terminal subagent details regressed: %#v", parts[0])
	}
}

func TestCollectedSubagentArtifactsAreReturnedOnlyOnce(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	control := &subagentControl{
		taskID: "subagent-artifact", cancel: func() {}, done: make(chan struct{}),
		latest: tools.ResultPart{Kind: tools.PartSubagent, TaskID: "subagent-artifact", Status: "running"},
	}
	service.subagents = map[string]*subagentControl{control.taskID: control}
	control.finish(delegatedTaskResult{
		part:      tools.ResultPart{Kind: tools.PartSubagent, TaskID: control.taskID, Status: "completed"},
		artifacts: []tools.ResultPart{{Kind: tools.PartDiff, Path: "one.go", Patch: "+one"}},
	})

	first, _ := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), json.RawMessage(`{}`))
	if countPartKind(first.Parts, tools.PartDiff) != 1 {
		t.Fatalf("first collection artifacts = %#v", first.Parts)
	}
	secondArgs := json.RawMessage(`{"taskIds":["subagent-artifact"]}`)
	second, _ := (AwaitSubagentsTool{Service: service}).Execute(context.Background(), secondArgs)
	if countPartKind(second.Parts, tools.PartDiff) != 0 {
		t.Fatalf("second collection repeated artifacts = %#v", second.Parts)
	}
	if final := service.finishSubagentTurn(false); countPartKind(final, tools.PartDiff) != 0 {
		t.Fatalf("turn cleanup repeated artifacts = %#v", final)
	}
}

func TestCancelledSubagentPartSurvivesToolErrorWrapping(t *testing.T) {
	provider := &subagentProbeProvider{delay: time.Millisecond}
	service, baseCtx := newSubagentToolTest(t, provider)
	ctx, cancel := context.WithCancel(baseCtx)
	cancel()
	call := protocol.ToolCall{ID: "delegate-cancelled", Type: "function", Function: protocol.ToolCallFunction{
		Name:      "delegate_task",
		Arguments: json.RawMessage(`{"tasks":[{"label":"取消测试","task":"不应开始","agentType":"explore"}]}`),
	}}
	result, _ := service.executeToolCall(ctx, service.buildToolRegistry(), call)
	assertSubagentParts(t, result.Parts, 1, "cancelled")
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func countPartKind(parts []tools.ResultPart, kind tools.PartKind) int {
	count := 0
	for _, part := range parts {
		if part.Kind == kind {
			count++
		}
	}
	return count
}

func assertSubagentToolSet(t *testing.T, definitions []protocol.ToolDefinition, writable bool) {
	t.Helper()
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		names[definition.Function.Name] = true
	}
	if names["delegate_task"] {
		t.Fatal("worker tool registry contains delegate_task")
	}
	if names["await_subagents"] {
		t.Fatal("worker tool registry contains await_subagents")
	}
	if names["update_plan"] {
		t.Fatal("worker tool registry contains main-plan coordinator")
	}
	if !names["read_file"] || !names["search"] {
		t.Fatalf("worker is missing read tools: %#v", names)
	}
	for _, name := range []string{"write_file", "apply_patch", "copy_file", "delete_file", "run_command", "terminal"} {
		if !writable && names[name] {
			t.Fatalf("read-only worker contains mutating tool %s", name)
		}
	}
	if writable && (!names["write_file"] || !names["apply_patch"]) {
		t.Fatalf("implement worker is missing write tools: %#v", names)
	}
}

func assertSubagentParts(t *testing.T, parts []tools.ResultPart, expected int, status string) {
	t.Helper()
	count := 0
	ids := make(map[string]bool)
	for _, part := range parts {
		if part.Kind != tools.PartSubagent {
			continue
		}
		count++
		if part.Status != status {
			t.Fatalf("subagent status = %q, want %q: %#v", part.Status, status, part)
		}
		if part.TaskID == "" || ids[part.TaskID] {
			t.Fatalf("invalid subagent task id: %#v", part)
		}
		ids[part.TaskID] = true
	}
	if count != expected {
		t.Fatalf("subagent part count = %d, want %d: %#v", count, expected, parts)
	}
}
