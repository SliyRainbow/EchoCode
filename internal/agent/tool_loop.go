package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// normalizeToolArgs 归一化模型返回的工具参数。
//
// OpenAI/DeepSeek function-calling 标准：tool_call 的 arguments 是「被 JSON 编码成字符串
// 的 JSON」，即 arguments 字段本身是一个字符串，如 "{\"path\":\".\"}"。
// 我们收到的 json.RawMessage 因此是带引号的字符串字面量，直接 Unmarshal 进 struct 会报
// "cannot unmarshal string into struct"。这里把这层字符串解开，还原为对象字节。
// 若本就是对象（个别 provider 直接给对象），原样返回。
func normalizeToolArgs(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	// 已是 JSON 对象/数组 → 原样返回。
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return raw
	}
	// 是被编码的字符串 → 先解出字符串内容（里面才是真正的 JSON）。
	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err == nil {
			inner = strings.TrimSpace(inner)
			if inner == "" {
				return json.RawMessage("{}")
			}
			return json.RawMessage(inner)
		}
	}
	return raw
}

// runToolLoopTurn 在工具循环之外套上一层会话簿记（与流式路径的收尾逻辑对齐）：
// 构造注册表、跑循环、写回 session、更新计数与指标、组装 ChatResult。
func (s *Service) runToolLoopTurn(
	ctx context.Context,
	streamProvider protocol.Provider,
	caller protocol.ToolCaller,
	baseRequest protocol.ChatRequest,
	route chatRoute,
	prefixDiagnostic requestPrefixDiagnostic,
	requestMessages []protocol.Message,
	baseMessageCount int,
	sink ChatEventSink,
) (ChatResult, error) {
	sink = serializedChatEventSink(sink)
	execRequest := baseRequest
	planStarted := false
	reg := s.buildToolRegistryForContext(ctx)

	// Plan 两段式：需用户显式开启 Plan 模式，且当前档位 Planner=true 时启用。
	// 默认关闭，避免每轮翻倍调用、破坏缓存经济性（符合真实工具的显式规划做法）。
	profile, _ := ReasoningProfileFor(s.reasoning)
	if s.planMode && !isGuidanceChatTurn(ctx) && profile.Budget.Planner && strings.TrimSpace(s.runtimeSettings.WorkspaceRoot) != "" {
		startChatTiming(ctx, "plan", "\u6b63\u5728\u751f\u6210\u6267\u884c\u8ba1\u5212", route.ModelID)
		emitChatEvent(sink, ChatStreamEvent{Type: "status", Message: "正在生成执行计划", Model: route.ModelID})
		planRequest := baseRequest
		plan, _, planErr := s.runPlanPhase(ctx, caller, planRequest, route, sink)
		if planErr != nil {
			s.sessionMessages = s.sessionMessages[:baseMessageCount]
			s.markChatProviderStatus(route.Provider.ID, "error", planErr.Error())
			return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, planErr
		}
		if plan != "" {
			planSteps := planStepsFromText(plan)
			if len(planSteps) > 0 {
				if planStateErr := s.startPlanState(planSteps); planStateErr != nil {
					s.sessionMessages = s.sessionMessages[:baseMessageCount]
					return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, planStateErr
				}
				planStarted = true
			}
			approved, apprErr := s.requestPlanApproval(ctx, plan)
			if apprErr != nil {
				s.sessionMessages = s.sessionMessages[:baseMessageCount]
				apprErr = s.failStartedPlan(planStarted, apprErr)
				return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, apprErr
			}
			if !approved {
				if planStarted {
					if planStateErr := s.finishPlanState("cancelled"); planStateErr != nil {
						return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, planStateErr
					}
				}
				// 用户否决计划：不执行，直接把计划作为回复返回。
				answer := "已生成执行计划，但你选择不执行。\n\n" + plan
				parts := []tools.ResultPart{{Kind: tools.PartText, Text: plan}}
				s.sessionMessages = s.appendProtocolAssistantMessage(s.sessionMessages, answer, parts)
				s.sessionState.MessageCount = len(s.sessionMessages)
				finishChatTiming(ctx, "completed")
				s.markChatProviderStatus(route.Provider.ID, "ok", "计划已生成，等待下一步。")
				return s.commitAssistantResult(ChatResult{
					Content: answer,
					Model:   route.ModelID,
					Usage:   s.metrics,
					State:   s.workbenchStateLocked(),
					Parts:   parts,
				}, chatTurnDurationMs(ctx))
			}
			if len(planSteps) > 0 {
				planSteps[0].Status = "in_progress"
				if planStateErr := s.updatePlanState(planSteps); planStateErr != nil {
					planStateErr = s.failStartedPlan(planStarted, planStateErr)
					return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, planStateErr
				}
			}
			// 批准：把计划注入执行阶段的消息尾部，让模型据此执行。
			execRequest.Messages = append(append([]protocol.Message{}, execRequest.Messages...),
				protocol.Message{Role: "assistant", Content: "执行计划：\n" + plan},
				protocol.Message{Role: "user", Content: "计划已批准，请按计划执行。"})
		}
	}

	emitChatEvent(sink, ChatStreamEvent{Type: "status", Message: "正在分析任务", Model: route.ModelID})
	startChatTiming(ctx, "execution", "\u6b63\u5728\u6267\u884c Agent \u4efb\u52a1", route.ModelID)
	executionCtx := withSubagentExecutionScope(ctx, subagentExecutionScope{
		BaseRequest:  execRequest,
		PrimaryRoute: route,
	})
	usageObserver := func(usage *protocol.TokenUsage) {
		usageRoute := resolvedProviderRoute(streamProvider, route)
		s.recordLiveUsage(usage, usageRoute, sink)
	}
	outcome, err := s.runStreamingToolLoopWithState(executionCtx, streamProvider, reg, execRequest, sink, usageObserver)
	resolvedRoute := resolvedProviderRoute(streamProvider, route)
	if resolvedRoute.Provider.ID != route.Provider.ID {
		route = resolvedRoute
		s.adoptProviderRoute(route)
	}
	if err == nil {
		if scopeErr := s.validateTurnTaskScopeOutcome(executionCtx, outcome); scopeErr != nil {
			message := "本轮未完成：用户指定的目标范围内没有检测到真实文件变更，已阻止将未完成任务报告为成功。"
			outcome.Content = message
			outcome.Parts = append(outcome.Parts, tools.ResultPart{Kind: tools.PartText, Text: message})
			emitChatEvent(sink, ChatStreamEvent{Type: "status", Message: message, Status: "failed"})
			err = scopeErr
		}
	}
	if err != nil {
		s.sessionMessages = s.sessionMessages[:baseMessageCount]
		s.markChatProviderStatus(route.Provider.ID, "error", err.Error())
		cancelled := chatTurnWasCancelled(ctx, err)
		if planStarted {
			if cancelled {
				if planErr := s.finishPlanState("cancelled"); planErr != nil {
					err = errors.Join(err, fmt.Errorf("cancel plan: %w", planErr))
				}
			} else {
				err = s.failStartedPlan(planStarted, err)
			}
		}
		terminalStatus := "failed"
		if cancelled {
			terminalStatus = "cancelled"
		}
		setOutcomeProgressStatus(&outcome, terminalStatus)
		emitOutcomeProgress(sink, outcome.Parts)

		partialContent := partialToolEvidenceContent(outcome)
		if partialContent == "" && !cancelled {
			partialContent = partialToolFailureContent(outcome)
		}
		if partialContent == "" && hasMeaningfulResultParts(outcome.Parts) {
			partialContent = retainedTurnContent(terminalStatus, "", outcome.Parts)
		}
		partialParts := appendTextPartIfMissing(outcome.Parts, partialContent)
		result := ChatResult{
			Content:   partialContent,
			Reasoning: outcome.Reasoning,
			Model:     route.ModelID,
			Usage:     s.metrics,
			State:     s.workbenchStateLocked(),
			Parts:     partialParts,
		}
		s.retainInterruptedTurn(&result, terminalStatus, requestMessages, baseMessageCount, prefixDiagnostic)
		return result, err
	}

	if planStarted {
		if planStateErr := s.finishPlanState("completed"); planStateErr != nil {
			return ChatResult{State: s.workbenchStateLocked(), Model: route.ModelID}, planStateErr
		}
	}

	answer := sanitizeModelContent(outcome.Content)
	s.sessionMessages = s.appendProtocolAssistantMessage(s.sessionMessages, answer, outcome.Parts)
	s.commitRequestPrefix(prefixDiagnostic, requestMessages)
	s.sessionState.MessageCount = len(s.sessionMessages)
	s.sessionState.TurnCount++
	// 文件快照已在每次工具写入后立即记录，这里只提交 assistant + checkpoint。
	finishChatTiming(ctx, "completed")
	s.markChatProviderStatus(route.Provider.ID, "ok", fmt.Sprintf("试聊成功，%s / %s 工具会话完成，产出 %d 个片段。", route.Provider.Name, route.ModelID, len(outcome.Parts)))

	return s.commitAssistantResult(ChatResult{
		Content:   answer,
		Reasoning: outcome.Reasoning,
		Model:     route.ModelID,
		Usage:     s.metrics,
		State:     s.workbenchStateLocked(),
		Parts:     outcome.Parts,
	}, chatTurnDurationMs(ctx))
}

func (s *Service) validateTurnTaskScopeOutcome(ctx context.Context, outcome toolLoopOutcome) error {
	scope := turnTaskScopeFrom(ctx)
	if !scope.Enabled || !scope.RequireWrite {
		return nil
	}
	policy := s.sandboxPolicyForContext(ctx)
	for _, change := range outcome.Changes {
		if _, err := policy.ResolveWritePath(change.Path); err == nil {
			return nil
		}
	}
	for _, part := range outcome.Parts {
		if strings.TrimSpace(part.Path) == "" {
			continue
		}
		switch part.Kind {
		case tools.PartDiff:
			if _, err := policy.ResolveWritePath(part.Path); err == nil {
				return nil
			}
		case tools.PartFile:
			if strings.EqualFold(strings.TrimSpace(part.FileAction), "available") {
				continue
			}
			if _, err := policy.ResolveWritePath(part.Path); err == nil {
				return nil
			}
		}
	}
	return errors.New("task scope requires a verified write, but no in-scope file change was recorded")
}

func partialToolFailureContent(outcome toolLoopOutcome) string {
	if content := partialToolEvidenceContent(outcome); content != "" {
		return content
	}
	if hasMeaningfulResultParts(outcome.Parts) {
		return "工具结果已经保留，但本轮任务尚未形成可用结论。请重试，或根据执行记录继续调查。"
	}
	return ""
}

func partialToolEvidenceContent(outcome toolLoopOutcome) string {
	return sanitizeModelContent(outcome.Content)
}

func hasWebSearchSources(parts []tools.ResultPart) bool {
	for _, part := range parts {
		if part.Kind == tools.PartWebSearch && len(part.Sources) > 0 {
			return true
		}
	}
	return false
}

func webSearchFallbackContent(part tools.ResultPart) string {
	var content strings.Builder
	content.WriteString("本轮未完成最终分析。以下仅为原始网络搜索记录，不能视为结论：")
	for index, source := range part.Sources {
		title := strings.TrimSpace(source.Title)
		if title == "" {
			title = strings.TrimSpace(source.URL)
		}
		fmt.Fprintf(&content, "\n\n%d. %s", index+1, title)
		if snippet := compactFallbackSnippet(source.Snippet, 220); snippet != "" {
			content.WriteString("\n   ")
			content.WriteString(snippet)
		}
		if source.URL != "" {
			content.WriteString("\n   ")
			content.WriteString(strings.TrimSpace(source.URL))
		}
	}
	return content.String()
}

func compactFallbackSnippet(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return value
}

func appendTextPartIfMissing(parts []tools.ResultPart, content string) []tools.ResultPart {
	content = strings.TrimSpace(content)
	if content == "" {
		return parts
	}
	for _, part := range parts {
		if part.Kind == tools.PartText && strings.TrimSpace(part.Text) == content {
			return parts
		}
	}
	return append(parts, tools.ResultPart{Kind: tools.PartText, Text: content})
}

// buildToolRegistry 依据当前 runtime settings 构造工具注册表。
// 沙盒策略从 RuntimeSettings 映射到 tools.SandboxPolicy，实现权限边界。
func (s *Service) buildToolRegistry() *tools.Registry {
	return s.buildToolRegistryForContext(context.Background())
}

func (s *Service) buildToolRegistryForContext(ctx context.Context) *tools.Registry {
	return s.buildMutableToolRegistryForContext(ctx, true, true)
}

// buildWorkerToolRegistry is used by delegated and fixed-team workers. It
// excludes coordinator tools so a worker cannot mutate the main plan or spawn
// another layer of agents.
func (s *Service) buildWorkerToolRegistry() *tools.Registry {
	return s.buildWorkerToolRegistryForContext(context.Background())
}

func (s *Service) buildMutableToolRegistry(includePlan, includeDelegation bool) *tools.Registry {
	return s.buildMutableToolRegistryForContext(context.Background(), includePlan, includeDelegation)
}

func (s *Service) buildWorkerToolRegistryForContext(ctx context.Context) *tools.Registry {
	return s.buildMutableToolRegistryForContext(ctx, false, false)
}

func (s *Service) buildMutableToolRegistryForContext(ctx context.Context, includePlan, includeDelegation bool) *tools.Registry {
	policy := s.sandboxPolicyForContext(ctx)
	readOnly := strings.EqualFold(strings.TrimSpace(policy.FilesystemAccess), "read-only") ||
		strings.EqualFold(strings.TrimSpace(policy.SandboxMode), "read-only")
	reg := tools.NewRegistry()
	reg.Add(ReportProgressTool{})
	reg.Add(LoadSkillTool{Service: s})
	reg.Add(DecodeProtectedSecretTool{Capture: s.storeSecretResult})
	if includePlan {
		reg.Add(tools.UpdatePlanTool{OnUpdate: s.updatePlanState})
	}
	reg.Add(tools.ReadFileTool{Policy: policy})
	reg.Add(tools.FileInfoTool{Policy: policy})
	reg.Add(tools.ListDirTool{Policy: policy})
	reg.Add(tools.SearchTool{Policy: policy})
	reg.AddStructuredSearch(policy)
	if s.config.ArtifactRenderer != nil {
		reg.Add(tools.RenderArtifactTool{Policy: policy, Controller: s})
		reg.Add(tools.InspectVisualTool{Policy: policy, Controller: s})
	}
	if s.runtimeSettings.NetworkAccess {
		reg.Add(tools.ReadRepositoryTool{Policy: policy})
		reg.Add(tools.ReadWebpageTool{Policy: policy, Browser: s.webpageBrowserRenderer()})
		reg.Add(tools.WebSearchTool{Policy: policy})
		if !readOnly {
			reg.Add(tools.DownloadFileTool{Policy: policy})
			reg.Add(tools.GitRepositoryTool{Policy: policy})
		}
	}
	if s.config.OpenFile != nil || s.config.PreviewFile != nil {
		reg.Add(tools.OpenFileTool{Policy: policy, Open: s.config.OpenFile, Preview: s.config.PreviewFile})
	}
	if !readOnly && s.config.Browser != nil && s.runtimeSettings.Browser.Enabled {
		reg.Add(tools.BrowserTool{Policy: policy, Controller: s.config.Browser})
	}
	computerSettings := s.runtimeSettings.ComputerControl
	if !readOnly && s.config.Computer != nil && (computerSettings.AnyAppEnabled || computerSettings.ChromeEnabled || len(computerSettings.AlwaysAllowedApps) > 0) {
		reg.Add(tools.ComputerTool{Policy: policy, Controller: s.config.Computer})
	}
	if includeDelegation {
		reg.Add(DelegateTaskTool{Service: s, ReadOnly: readOnly})
		reg.Add(AwaitSubagentsTool{Service: s})
	}
	if !readOnly {
		reg.Add(tools.WriteFileTool{Policy: policy})
		reg.Add(tools.ApplyPatchTool{Policy: policy})
		reg.Add(tools.CopyFileTool{Policy: policy})
		reg.Add(tools.DeleteFileTool{Policy: policy})
	}
	// run_command 仅在 ShellAccess 开启时注册（文件操作绝不经 shell）。
	if s.runtimeSettings.ShellAccess && !readOnly {
		reg.Add(tools.RunCommandTool{Policy: policy})
	}
	if s.runtimeSettings.NetworkAccess && s.runtimeSettings.ShellAccess && !readOnly {
		reg.Add(SSHCredentialTool{
			Policy:         policy,
			Resolve:        s.resolveScopedSSHCredential,
			CaptureSecret:  s.storeSecretResult,
			KnownHostsPath: s.scopedSSHKnownHostsPath(),
		})
	}
	// A scoped turn may still inspect repository state, but Git mutations and
	// persistent terminal sessions cannot be constrained to one target path.
	if s.config.Git != nil {
		reg.Add(GitTool{Policy: policy, Controller: s.config.Git, ReadOnlyOnly: readOnly || policy.TaskScopeEnabled})
	}
	if s.config.Terminal != nil && s.runtimeSettings.ShellAccess && !readOnly && !policy.TaskScopeEnabled {
		reg.Add(TerminalTool{Policy: policy, Controller: s.config.Terminal})
	}
	// Read-only MCP tools remain useful for research in a scoped turn. Mutating
	// MCP tools stay hidden because their local path boundary is not generally
	// enforceable by the host.
	if s.mcpManager != nil {
		for _, remoteTool := range s.mcpManager.ToolsForWorkspace(s.runtimeSettings.WorkspaceRoot) {
			if readOnly || policy.TaskScopeEnabled {
				readOnlyTool, ok := remoteTool.(interface{ ReadOnly() bool })
				if !ok || !readOnlyTool.ReadOnly() {
					continue
				}
			}
			reg.Add(remoteTool)
		}
	}
	if s.pluginManager != nil {
		for _, pluginTool := range s.pluginManager.Tools(s.runtimeSettings.Plugins, policy, readOnly) {
			reg.Add(pluginTool)
		}
	}
	return reg
}

// buildReadOnlyRegistry 只含只读工具，供 Plan 阶段「先探索、不改动」使用。
func (s *Service) buildReadOnlyRegistry() *tools.Registry {
	return s.buildReadOnlyRegistryForContext(context.Background())
}

func (s *Service) buildReadOnlyRegistryForContext(ctx context.Context) *tools.Registry {
	policy := s.sandboxPolicyForContext(ctx)
	policy.SandboxMode = "read-only"
	policy.FilesystemAccess = "read-only"
	policy.ShellAccess = false
	policy.AllowDestructiveOps = false
	reg := tools.NewRegistry(
		ReportProgressTool{},
		LoadSkillTool{Service: s},
		DecodeProtectedSecretTool{Capture: s.storeSecretResult},
		tools.ReadFileTool{Policy: policy},
		tools.FileInfoTool{Policy: policy},
		tools.ListDirTool{Policy: policy},
		tools.SearchTool{Policy: policy},
	)
	reg.AddStructuredSearch(policy)
	if s.config.ArtifactRenderer != nil {
		reg.Add(tools.RenderArtifactTool{Policy: policy, Controller: s})
		reg.Add(tools.InspectVisualTool{Policy: policy, Controller: s})
	}
	if s.config.Git != nil {
		reg.Add(GitTool{Policy: policy, Controller: s.config.Git, ReadOnlyOnly: true})
	}
	if s.runtimeSettings.NetworkAccess {
		reg.Add(tools.ReadRepositoryTool{Policy: policy})
		reg.Add(tools.ReadWebpageTool{Policy: policy, Browser: s.webpageBrowserRenderer()})
		reg.Add(tools.WebSearchTool{Policy: policy})
	}
	if s.mcpManager != nil {
		for _, remoteTool := range s.mcpManager.ToolsForWorkspace(s.runtimeSettings.WorkspaceRoot) {
			readOnlyTool, ok := remoteTool.(interface{ ReadOnly() bool })
			if ok && readOnlyTool.ReadOnly() {
				reg.Add(remoteTool)
			}
		}
	}
	if s.pluginManager != nil {
		for _, pluginTool := range s.pluginManager.Tools(s.runtimeSettings.Plugins, policy, true) {
			reg.Add(pluginTool)
		}
	}
	return reg
}

func (s *Service) webpageBrowserRenderer() tools.WebpageBrowserRenderer {
	if s.config.Browser == nil || !s.runtimeSettings.Browser.Enabled {
		return nil
	}
	renderer, _ := s.config.Browser.(tools.WebpageBrowserRenderer)
	return renderer
}

// toolDefinitions 把注册表的工具 schema 转成 provider 需要的 protocol.ToolDefinition。
func toolDefinitions(reg *tools.Registry) []protocol.ToolDefinition {
	schemas := reg.Schemas()
	defs := make([]protocol.ToolDefinition, 0, len(schemas))
	for _, sc := range schemas {
		defs = append(defs, protocol.ToolDefinition{
			Type: sc.Type,
			Function: protocol.ToolDefinitionFunc{
				Name:        sc.Function.Name,
				Description: sc.Function.Description,
				Parameters:  sc.Function.Parameters,
			},
		})
	}
	return defs
}

// toolLoopOutcome 汇总一次工具循环的最终结果。
type toolLoopOutcome struct {
	Content      string
	Reasoning    string
	Parts        []tools.ResultPart
	Changes      []tools.FileChange
	Usage        *protocol.TokenUsage
	UsageSamples []protocol.TokenUsage
}

// runToolLoop 执行「补全 → 若有 tool_calls 则执行并回喂 → 再补全」的循环，
// 直到模型给出最终文本、用户取消或循环保护判定没有继续执行价值。
// caller 为支持 function-calling 的 provider；baseMessages 为初始对话（含用户输入）。
func (s *Service) runToolLoop(
	ctx context.Context,
	caller protocol.ToolCaller,
	reg *tools.Registry,
	req protocol.ChatRequest,
) (toolLoopOutcome, error) {
	return s.runToolLoopWithCompletion(ctx, reg, req, caller.Complete, nil)
}

func (s *Service) runStreamingToolLoop(
	ctx context.Context,
	provider protocol.Provider,
	reg *tools.Registry,
	req protocol.ChatRequest,
	sink ChatEventSink,
) (toolLoopOutcome, error) {
	return s.runStreamingToolLoopWithState(ctx, provider, reg, req, sink, nil)
}

type usageObserver func(*protocol.TokenUsage)

func (s *Service) runStreamingToolLoopWithState(
	ctx context.Context,
	provider protocol.Provider,
	reg *tools.Registry,
	req protocol.ChatRequest,
	sink ChatEventSink,
	observeUsage usageObserver,
) (toolLoopOutcome, error) {
	complete := func(ctx context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		completion, err := collectProviderStream(ctx, provider, request, sink)
		if completion.Usage != nil && observeUsage != nil {
			usage := *completion.Usage
			observeUsage(&usage)
		}
		return completion, err
	}
	return s.runToolLoopWithCompletionState(ctx, reg, req, complete, sink)
}

type completionFunc func(context.Context, protocol.ChatRequest) (protocol.CompletionResult, error)

const postToolCompletionRetries = 2

const maxEmptyCompletionRecoveries = 4

const toolResultRecoveryKind = "tool-result-recovery"

var errEmptyToolResultSynthesis = errors.New("模型在工具执行后没有返回可用结论")

const (
	toolLoopCycleRepetitions = 3
	toolLoopMaxCyclePeriod   = 3
)

type completedWebSearch struct {
	signature string
	query     string
	sources   []tools.SearchSource
}

type toolLoopGuard struct {
	searches           []completedWebSearch
	browserUnavailable bool
	completedSSHCalls  map[string]bool
	lastPlanSignature  string
	forceFinalResponse bool
	failureStrategy    failureStrategyState
	resolvedFailures   map[string]bool
	blockedFailures    map[string]int
	turnIndex          int
	cycles             toolLoopCycleGuard
}

type toolLoopCycleGuard struct {
	history []string
}

type toolLoopRoundRecord struct {
	Name      string
	Arguments json.RawMessage
	Result    tools.Result
}

type stableToolLoopRecord struct {
	Name        string                     `json:"name"`
	Arguments   string                     `json:"arguments"`
	Summary     string                     `json:"summary"`
	IsError     bool                       `json:"isError"`
	Parts       []tools.ResultPart         `json:"parts,omitempty"`
	Changes     []stableToolLoopChange     `json:"changes,omitempty"`
	Attachments []stableToolLoopAttachment `json:"attachments,omitempty"`
}

type stableToolLoopChange struct {
	Path            string `json:"path"`
	BeforeHash      string `json:"beforeHash"`
	AfterHash       string `json:"afterHash"`
	Existed         bool   `json:"existed"`
	Deleted         bool   `json:"deleted"`
	LineEnding      string `json:"lineEnding"`
	Encoding        string `json:"encoding"`
	HadBOM          bool   `json:"hadBom"`
	AfterLineEnding string `json:"afterLineEnding"`
	AfterEncoding   string `json:"afterEncoding"`
	AfterHadBOM     bool   `json:"afterHadBom"`
}

type stableToolLoopAttachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	DataHash string `json:"dataHash"`
}

func newAwaitSubagentsToolCall(wait bool) protocol.ToolCall {
	arguments := `{"wait":false}`
	if wait {
		arguments = `{"wait":true}`
	}
	return protocol.ToolCall{
		ID:   fmt.Sprintf("await-subagents-%d", time.Now().UnixNano()),
		Type: "function",
		Function: protocol.ToolCallFunction{
			Name:      "await_subagents",
			Arguments: json.RawMessage(arguments),
		},
	}
}

func (g *toolLoopGuard) before(call protocol.ToolCall) (tools.Result, protocol.Message, bool, bool) {
	name := call.Function.Name
	input := toolInputForDisplay(name, call.Function.Arguments)
	if name == "update_plan" {
		signature := normalizedToolArguments(call.Function.Arguments)
		if signature != "" && signature == g.lastPlanSignature {
			summary := "计划内容与状态没有变化，已跳过重复更新。"
			return tools.Result{Summary: summary}, protocol.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: summary}, true, true
		}
	}
	if result, message, blocked := g.beforeEquivalentFailure(call); blocked {
		return result, message, true, false
	}
	if name == "ssh" {
		signature := sshToolCallSignature(call.Function.Arguments)
		if signature != "" && g.completedSSHCalls[signature] && strings.HasPrefix(signature, "test\x00") {
			summary := "相同的 SSH 操作本轮已经成功执行，已跳过重复调用；请复用先前结果继续任务。"
			return tools.Result{Summary: summary}, protocol.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: summary}, true, true
		}
	}
	if name == "browser" && g.browserUnavailable {
		summary := "内置浏览器本轮仍不可用，已跳过重复启动。请根据当前任务选择其他可用工具继续核验，或明确说明页面尚无法核验。"
		result := tools.Result{
			Summary: summary,
			IsError: true,
			Parts: []tools.ResultPart{{
				Kind: tools.PartToolCall, Name: name, Status: "error", Input: input, Output: summary,
			}},
		}
		return result, protocol.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: summary}, true, false
	}
	if name != "web_search" || strings.TrimSpace(input) == "" {
		return tools.Result{}, protocol.Message{}, false, false
	}
	signature := webSearchRequestSignature(call.Function.Arguments)
	if signature == "" {
		return tools.Result{}, protocol.Message{}, false, false
	}
	for _, search := range g.searches {
		if signature != search.signature {
			continue
		}
		summary := fmt.Sprintf("本轮已完成相近搜索 %q，复用已有 %d 条来源，未重复联网。", search.query, len(search.sources))
		result := tools.Result{
			Summary: summary,
			Parts: []tools.ResultPart{
				{Kind: tools.PartToolCall, Name: name, Status: "ok", Input: input, Output: summary},
				{Kind: tools.PartWebSearch, Query: search.query, Sources: append([]tools.SearchSource(nil), search.sources...)},
			},
		}
		return result, protocol.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: summary}, true, false
	}
	return tools.Result{}, protocol.Message{}, false, false
}

func (g *toolLoopGuard) after(call protocol.ToolCall, result tools.Result, message *protocol.Message) {
	if result.IsError {
		record, diagnosis := g.failureStrategy.observeFailure(call, result, g.turnIndex)
		if record.StrategyKey != "" {
			feedback := failureDiagnosticContent("change_strategy_before_retry", record, diagnosis)
			message.Content = strings.TrimSpace(message.Content + "\n\nEchoCode structured failure diagnosis:\n" + feedback)
			if record.Retryable && record.Attempts >= equivalentFailureRepetitions {
				g.forceFinalResponse = true
			}
		}
	} else {
		g.failureStrategy.observeSuccess(call, result, g.resolvedFailures)
	}
	switch call.Function.Name {
	case "update_plan":
		if result.IsError {
			return
		}
		g.lastPlanSignature = normalizedToolArguments(call.Function.Arguments)
	case "ssh":
		if result.IsError {
			return
		}
		if g.completedSSHCalls == nil {
			g.completedSSHCalls = make(map[string]bool)
		}
		if signature := sshToolCallSignature(call.Function.Arguments); strings.HasPrefix(signature, "test\x00") {
			g.completedSSHCalls[signature] = true
		}
	case "web_search":
		if result.IsError {
			return
		}
		sources := searchSourcesFromParts(result.Parts)
		if len(sources) == 0 {
			return
		}
		signature := webSearchRequestSignature(call.Function.Arguments)
		if signature == "" {
			return
		}
		g.searches = append(g.searches, completedWebSearch{
			signature: signature,
			query:     toolInputForDisplay("web_search", call.Function.Arguments),
			sources:   sources,
		})
	case "browser":
		if !result.IsError || !browserEngineUnavailable(result.Summary) {
			return
		}
		g.browserUnavailable = true
		message.Content = strings.TrimSpace(message.Content + "\n本轮不要再次调用 browser；请选择其他可用工具继续核验，若没有可用替代方案则明确说明受阻原因。")
	}
}

const equivalentFailureRepetitions = 3

func equivalentToolArgumentShape(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(normalizeToolArgs(raw), &value); err != nil {
		return normalizeEquivalentText(string(raw))
	}
	value = normalizeEquivalentValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return normalizeEquivalentText(string(raw))
	}
	return string(encoded)
}

func normalizeEquivalentValue(value any) any {
	switch typed := value.(type) {
	case string:
		return normalizeEquivalentText(typed)
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = normalizeEquivalentValue(item)
		}
		return items
	case map[string]any:
		items := make(map[string]any, len(typed))
		for key, item := range typed {
			items[key] = normalizeEquivalentValue(item)
		}
		return items
	default:
		return value
	}
}

func normalizeEquivalentText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("\"", "", "'", "", "`", "", "\\", "/").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func normalizedToolArguments(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(normalizeToolArgs(raw), &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(encoded)
}

func (g *toolLoopCycleGuard) observe(records []toolLoopRoundRecord) (int, bool) {
	fingerprint := toolLoopRoundFingerprint(records)
	if fingerprint == "" {
		return 0, false
	}
	g.history = append(g.history, fingerprint)
	maxHistory := toolLoopCycleRepetitions * toolLoopMaxCyclePeriod
	if len(g.history) > maxHistory {
		g.history = append([]string(nil), g.history[len(g.history)-maxHistory:]...)
	}
	for period := 1; period <= toolLoopMaxCyclePeriod; period++ {
		needed := period * toolLoopCycleRepetitions
		if len(g.history) < needed {
			continue
		}
		window := g.history[len(g.history)-needed:]
		repeated := true
		for index := period; index < len(window); index++ {
			if window[index] != window[index%period] {
				repeated = false
				break
			}
		}
		if repeated {
			return period, true
		}
	}
	return 0, false
}

func toolLoopRoundFingerprint(records []toolLoopRoundRecord) string {
	if len(records) == 0 {
		return ""
	}
	stable := make([]stableToolLoopRecord, 0, len(records))
	for _, record := range records {
		parts := append([]tools.ResultPart(nil), record.Result.Parts...)
		for index := range parts {
			parts[index].ToolCallID = ""
			parts[index].StartedAt = ""
			parts[index].CompletedAt = ""
			parts[index].DurationMs = 0
			parts[index].RequestID = ""
			parts[index].TaskID = ""
			parts[index].SecretID = ""
			parts[index].Activities = append([]tools.SubagentActivity(nil), parts[index].Activities...)
			for activityIndex := range parts[index].Activities {
				parts[index].Activities[activityIndex].ID = ""
				parts[index].Activities[activityIndex].StartedAt = ""
				parts[index].Activities[activityIndex].CompletedAt = ""
				parts[index].Activities[activityIndex].DurationMs = 0
			}
		}
		changes := make([]stableToolLoopChange, 0, len(record.Result.Changes))
		for _, change := range record.Result.Changes {
			changes = append(changes, stableToolLoopChange{
				Path:            change.Path,
				BeforeHash:      toolLoopContentHash(change.Before),
				AfterHash:       toolLoopContentHash(change.After),
				Existed:         change.Existed,
				Deleted:         change.Deleted,
				LineEnding:      change.LineEnding,
				Encoding:        change.Encoding,
				HadBOM:          change.HadBOM,
				AfterLineEnding: change.AfterLineEnding,
				AfterEncoding:   change.AfterEncoding,
				AfterHadBOM:     change.AfterHadBOM,
			})
		}
		attachments := make([]stableToolLoopAttachment, 0, len(record.Result.Attachments))
		for _, attachment := range record.Result.Attachments {
			attachments = append(attachments, stableToolLoopAttachment{
				Name:     attachment.Name,
				MIMEType: attachment.MIMEType,
				DataHash: toolLoopContentHash(attachment.Data),
			})
		}
		stable = append(stable, stableToolLoopRecord{
			Name:        record.Name,
			Arguments:   normalizedToolArguments(record.Arguments),
			Summary:     strings.TrimSpace(record.Result.Summary),
			IsError:     record.Result.IsError,
			Parts:       parts,
			Changes:     changes,
			Attachments: attachments,
		})
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return ""
	}
	return toolLoopContentHash(string(encoded))
}

func toolLoopContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum[:])
}

func toolLoopCycleFeedback(period int) string {
	if period <= 1 {
		return "安全熔断：相同的工具调用和结果已经连续重复，任务没有产生新进展。请停止调用工具，基于已有结果说明当前状态、已完成内容和建议的下一步。"
	}
	return fmt.Sprintf("安全熔断：检测到重复的 %d 步工具调用周期，结果没有产生新进展。请停止调用工具，基于已有结果说明当前状态、已完成内容和建议的下一步。", period)
}

func appendToolLoopFeedback(messages []protocol.Message, feedback string) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "tool" {
			continue
		}
		messages[index].Content = strings.TrimSpace(messages[index].Content + "\n\n" + feedback)
		return
	}
}

func sshToolCallSignature(raw json.RawMessage) string {
	var args sshToolArguments
	if err := json.Unmarshal(normalizeToolArgs(raw), &args); err != nil {
		return ""
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	credentialID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args.CredentialID), scopedCredentialScheme))
	switch action {
	case "test":
		return action + "\x00" + credentialID
	case "run", "capture_secret":
		command := strings.ToLower(strings.Join(strings.Fields(args.Command), " "))
		if command == "" {
			return ""
		}
		return action + "\x00" + credentialID + "\x00" + command
	default:
		return ""
	}
}

func searchSourcesFromParts(parts []tools.ResultPart) []tools.SearchSource {
	for _, part := range parts {
		if part.Kind == tools.PartWebSearch && len(part.Sources) > 0 {
			return append([]tools.SearchSource(nil), part.Sources...)
		}
	}
	return nil
}

func browserEngineUnavailable(summary string) bool {
	return strings.Contains(summary, "内置浏览器启动失败") || strings.Contains(summary, "启动浏览器引擎失败")
}

func canonicalSearchQuery(query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ""
	}
	return strings.Join(strings.Fields(query), " ")
}

func webSearchRequestSignature(raw json.RawMessage) string {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(normalizeToolArgs(raw), &args); err != nil {
		return ""
	}
	args.Query = canonicalSearchQuery(args.Query)
	if args.Query == "" {
		return ""
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 6
	}
	if args.MaxResults > 10 {
		args.MaxResults = 10
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (s *Service) runToolLoopWithCompletion(
	ctx context.Context,
	reg *tools.Registry,
	req protocol.ChatRequest,
	complete completionFunc,
	sink ChatEventSink,
) (toolLoopOutcome, error) {
	return s.runToolLoopWithCompletionState(ctx, reg, req, complete, sink)
}

func (s *Service) runToolLoopWithCompletionState(
	ctx context.Context,
	reg *tools.Registry,
	req protocol.ChatRequest,
	complete completionFunc,
	sink ChatEventSink,
) (toolLoopOutcome, error) {
	outcome := toolLoopOutcome{}
	messages := append([]protocol.Message{}, req.Messages...)
	toolDefs := toolDefinitions(reg)
	toolsDisabled := false // 自定义模型不支持 function-calling 时降级为纯对话
	emptyCompletionStage := 0
	synthesisOnly := false
	forceSubstantiveTool := false
	requiredToolChoiceUnsupported := false
	visualVerificationRequested := false
	subagentFinalizationPrompted := false
	guard := toolLoopGuard{
		completedSSHCalls: make(map[string]bool),
		failureStrategy:   s.failureStrategySnapshot(),
		resolvedFailures:  make(map[string]bool),
		blockedFailures:   make(map[string]int),
		turnIndex:         s.sessionState.TurnCount + 1,
	}
	defer func() {
		s.mergeFailureStrategyState(guard.failureStrategy, guard.resolvedFailures)
	}()
	executed := 0
	for {
		if err := ctx.Err(); err != nil {
			setOutcomeProgressStatus(&outcome, "cancelled")
			emitOutcomeProgress(sink, outcome.Parts)
			return outcome, err
		}
		stepReq := req
		stepReq.Messages = fitToolLoopMessages(messages, req.TargetInputTokens)
		if req.MaxInputTokens > 0 && estimateProtocolMessagesTokens(stepReq.Messages) > req.MaxInputTokens {
			return outcome, fmt.Errorf("压缩后的工具上下文仍超过模型输入预算 %d tokens；请缩短输入或选择更大上下文模型", req.MaxInputTokens)
		}
		if !toolsDisabled {
			stepReq.Tools = toolDefs
			if forceSubstantiveTool && !requiredToolChoiceUnsupported {
				stepReq.ToolChoice = "required"
			}
		} else {
			stepReq.Tools = nil
			stepReq.ToolChoice = "none"
		}

		completion, err := complete(ctx, stepReq)
		if err != nil && executed > 0 && isRetryablePostToolCompletionError(ctx, err) {
			for retry := 1; retry <= postToolCompletionRetries; retry++ {
				emitChatEvent(sink, ChatStreamEvent{
					Type:    "status",
					Message: fmt.Sprintf("模型连接中断，正在继续整理工具结果（%d/%d）", retry, postToolCompletionRetries),
					Status:  "retrying",
				})
				if waitErr := waitForPostToolRetry(ctx, retry); waitErr != nil {
					err = waitErr
					break
				}
				completion, err = complete(ctx, stepReq)
				if err == nil || !isRetryablePostToolCompletionError(ctx, err) {
					break
				}
			}
		}
		completion.Content = visibleCompletionContent(completion.Content, completion.ToolCalls)
		outcome.Parts = mergeOutcomeParts(outcome.Parts, providerNoticeParts(completion.Notices))
		if completion.Usage != nil {
			usage := *completion.Usage
			outcome.Usage = &usage
			outcome.UsageSamples = append(outcome.UsageSamples, usage)
		}
		if strings.TrimSpace(completion.Reasoning) != "" {
			if outcome.Reasoning != "" {
				outcome.Reasoning += "\n\n"
			}
			outcome.Reasoning += completion.Reasoning
		}
		if strings.TrimSpace(completion.Content) != "" {
			outcome.Content = completion.Content
		}
		if err != nil && stepReq.ParallelToolCalls && isParallelToolCallsCompatibilityError(err) {
			req.ParallelToolCalls = false
			req.OmitParallelToolCalls = true
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "status",
				Message: "当前模型端点不支持并行工具请求，正在保留完整工具能力并以兼容模式重试。",
				Status:  "retrying",
			})
			continue
		}
		if err != nil && stepReq.ToolChoice == "required" && isRequiredToolChoiceCompatibilityError(err) {
			requiredToolChoiceUnsupported = true
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "status",
				Message: "当前模型端点不支持强制工具选择，正在保留完整工具能力并以自动模式继续。",
				Status:  "retrying",
			})
			continue
		}
		if err != nil {
			// Only retry without tools when the provider explicitly rejects the
			// function-calling fields. Transport, timeout, policy, authentication,
			// and unrelated request errors must retain their real failure semantics.
			if !toolsDisabled && executed == 0 && len(toolDefs) > 0 && isToolCompatibilityError(err) {
				emitChatEvent(sink, ChatStreamEvent{
					Type:    "status",
					Message: "当前模型端点明确不支持工具调用，已降级为普通对话。",
					Status:  "retrying",
				})
				toolsDisabled = true
				continue
			}
			setOutcomeProgressStatus(&outcome, "failed")
			emitOutcomeProgress(sink, outcome.Parts)
			return outcome, err
		}

		// 无工具调用 → 最终答案。
		if toolsDisabled && len(completion.ToolCalls) > 0 {
			if synthesisOnly {
				if content := strings.TrimSpace(completion.Content); content != "" {
					outcome.Content = s.appendVisualVerificationDisclosure(content)
					outcome.Parts = append(outcome.Parts, tools.ResultPart{Kind: tools.PartText, Text: outcome.Content})
					setOutcomeProgressStatus(&outcome, "completed")
					emitOutcomeProgress(sink, outcome.Parts)
					return outcome, nil
				}
				setOutcomeProgressStatus(&outcome, "failed")
				emitOutcomeProgress(sink, outcome.Parts)
				return outcome, errEmptyToolResultSynthesis
			}
			summary := "上游模型在工具已禁用后仍请求调用工具，EchoCode 已停止本轮工具循环并保留全部已完成结果。"
			if _, canCollectSubagents := reg.Get("await_subagents"); canCollectSubagents && s.hasUncollectedSubagents() {
				call := newAwaitSubagentsToolCall(true)
				result, _ := s.executeToolCall(ctx, reg, call)
				outcome.Parts = mergeOutcomeParts(outcome.Parts, result.Parts)
				if strings.TrimSpace(result.Summary) != "" {
					summary += "\n\n" + result.Summary
				}
				if err := ctx.Err(); err != nil {
					setOutcomeProgressStatus(&outcome, "cancelled")
					emitOutcomeProgress(sink, outcome.Parts)
					return outcome, err
				}
			}
			emitChatEvent(sink, ChatStreamEvent{Type: "status", Message: summary, Status: "failed"})
			outcome.Content = strings.TrimSpace(completion.Content)
			if outcome.Content != "" {
				outcome.Content = s.appendVisualVerificationDisclosure(outcome.Content)
				outcome.Parts = append(outcome.Parts, tools.ResultPart{Kind: tools.PartText, Text: outcome.Content})
			}
			setOutcomeProgressStatus(&outcome, "failed")
			emitOutcomeProgress(sink, outcome.Parts)
			return outcome, errProviderIgnoredDisabledTools
		}

		if len(completion.ToolCalls) == 0 {
			_, canCollectSubagents := reg.Get("await_subagents")
			if canCollectSubagents && s.hasUncollectedSubagents() {
				if !subagentFinalizationPrompted {
					subagentFinalizationPrompted = true
					messages = append(messages,
						protocol.Message{Role: "assistant", Content: completion.Content, ReasoningContent: completion.Reasoning},
						pendingSubagentRecoveryMessage(),
					)
					emitChatEvent(sink, ChatStreamEvent{
						Type:    "status",
						Message: "后台子代理仍在运行，主 Agent 正在继续可独立推进的工作",
						Status:  "running",
					})
					continue
				}
				call := newAwaitSubagentsToolCall(true)
				messages = append(messages, protocol.Message{
					Role:             "assistant",
					Content:          completion.Content,
					ReasoningContent: completion.Reasoning,
					ToolCalls:        []protocol.ToolCall{call},
				})
				executed++
				emitChatEvent(sink, ChatStreamEvent{
					Type:       "tool",
					Message:    "正在等待后台子代理完成",
					ToolName:   call.Function.Name,
					ToolCallID: call.ID,
					Status:     "waiting",
				})
				result, toolMessage := s.executeToolCall(ctx, reg, call)
				toolStatus := "completed"
				if result.IsError {
					toolStatus = "error"
				}
				emitChatEvent(sink, ChatStreamEvent{
					Type:       "tool",
					Message:    result.Summary,
					ToolName:   call.Function.Name,
					ToolCallID: call.ID,
					Status:     toolStatus,
					Parts:      result.Parts,
				})
				outcome.Parts = mergeOutcomeParts(outcome.Parts, result.Parts)
				messages = append(messages, toolMessage)
				if err := ctx.Err(); err != nil {
					setOutcomeProgressStatus(&outcome, "cancelled")
					emitOutcomeProgress(sink, outcome.Parts)
					return outcome, err
				}
				continue
			}
			pendingVisual := s.pendingVisualArtifactsForCurrentTurn()
			if len(pendingVisual) > 0 && !visualVerificationRequested && !toolsDisabled {
				visualVerificationRequested = true
				messages = append(messages, visualVerificationRecoveryMessage(pendingVisual))
				emitChatEvent(sink, ChatStreamEvent{
					Type:    "status",
					Message: "发现尚未完成视觉验收的产物，正在请求渲染与检查",
					Status:  "running",
				})
				continue
			}
			content := strings.TrimSpace(completion.Content)
			if content == "" {
				hasToolEvidence := executed > 0 || hasMeaningfulResultParts(outcome.Parts)
				if hasToolEvidence && toolsDisabled && !synthesisOnly {
					synthesisOnly = true
					messages = append(messages, toolResultRecoveryMessage(true))
					emitChatEvent(sink, ChatStreamEvent{
						Type:    "status",
						Message: "正在执行任务",
						Status:  "retrying",
					})
					continue
				}
				if hasToolEvidence && !toolsDisabled {
					emptyCompletionStage++
					if emptyCompletionStage >= maxEmptyCompletionRecoveries {
						setOutcomeProgressStatus(&outcome, "failed")
						emitOutcomeProgress(sink, outcome.Parts)
						return outcome, errEmptyToolResultSynthesis
					}
					if emptyCompletionStage >= 2 {
						forceSubstantiveTool = true
					}
					messages = append(messages, toolResultRecoveryMessage(false))
					emitChatEvent(sink, ChatStreamEvent{
						Type:    "status",
						Message: "正在执行任务",
						Status:  "running",
					})
					continue
				}
				setOutcomeProgressStatus(&outcome, "failed")
				emitOutcomeProgress(sink, outcome.Parts)
				return outcome, errEmptyToolResultSynthesis
			}
			outcome.Content = s.appendVisualVerificationDisclosure(content)
			if strings.TrimSpace(outcome.Content) != "" {
				outcome.Parts = append(outcome.Parts, tools.ResultPart{
					Kind: tools.PartText,
					Text: outcome.Content,
				})
			}
			setOutcomeProgressStatus(&outcome, "completed")
			emitOutcomeProgress(sink, outcome.Parts)
			return outcome, nil
		}

		// 记录 assistant 的工具调用轮（回喂需要）。
		messages = append(messages, protocol.Message{
			Role:             "assistant",
			Content:          completion.Content,
			ReasoningContent: completion.Reasoning,
			ToolCalls:        completion.ToolCalls,
			Continuation:     completion.Continuation,
		})
		if strings.TrimSpace(completion.Content) != "" {
			outcome.Parts = append(outcome.Parts, tools.ResultPart{Kind: tools.PartText, Text: completion.Content})
		}

		// 逐个执行工具调用。
		roundRecords := make([]toolLoopRoundRecord, 0, len(completion.ToolCalls))
		roundHasSubstantiveTool := false
		for _, call := range completion.ToolCalls {
			if err := ctx.Err(); err != nil {
				setOutcomeProgressStatus(&outcome, "cancelled")
				emitOutcomeProgress(sink, outcome.Parts)
				return outcome, err
			}
			isProgressUpdate := isModelProgressTool(call.Function.Name)
			toolInput := toolInputForDisplay(call.Function.Name, call.Function.Arguments)
			result, toolMsg, guarded, hidden := guard.before(call)
			if !isProgressUpdate && !hidden {
				executed++
				roundHasSubstantiveTool = true
			}
			if !isProgressUpdate && !hidden {
				emitChatEvent(sink, ChatStreamEvent{
					Type:       "tool",
					Message:    fmt.Sprintf("正在运行 %s", call.Function.Name),
					ToolName:   call.Function.Name,
					ToolCallID: call.ID,
					ToolInput:  toolInput,
					Status:     "running",
				})
			}

			if !guarded {
				toolCtx := tools.WithProgressSink(ctx, func(part tools.ResultPart) {
					if part.Kind == tools.PartSubagent {
						message := strings.TrimSpace(part.CurrentAction)
						if message == "" {
							message = strings.TrimSpace(part.Summary)
						}
						if message == "" {
							message = "子代理正在工作"
						}
						emitChatEvent(sink, ChatStreamEvent{
							Type:    "subagent",
							Message: message,
							Status:  part.Status,
							Parts:   []tools.ResultPart{part},
						})
						return
					}
					if part.Kind == tools.PartTimelineNote {
						message := strings.TrimSpace(part.Message)
						if message == "" {
							return
						}
						status := strings.TrimSpace(part.Status)
						if status == "" {
							status = "running"
						}
						emitChatEvent(sink, ChatStreamEvent{
							Type: "status", Message: message, Status: status,
							ToolCallID: call.ID, Parts: []tools.ResultPart{part},
						})
						return
					}
					if part.Name == "" {
						part.Name = call.Function.Name
					}
					if part.Input == "" {
						part.Input = toolInput
					}
					if part.ToolCallID == "" {
						part.ToolCallID = call.ID
					}
					if part.Status == "" {
						part.Status = "running"
					}
					eventStatus := strings.TrimSpace(part.Status)
					if eventStatus == "" {
						eventStatus = "running"
					}
					emitChatEvent(sink, ChatStreamEvent{
						Type:       "tool",
						Message:    toolProgressMessage(call.Function.Name, eventStatus, part.Output),
						ToolName:   call.Function.Name,
						ToolCallID: call.ID,
						ToolInput:  toolInput,
						Status:     eventStatus,
						Parts:      []tools.ResultPart{part},
					})
				})
				result, toolMsg = s.executeToolCall(toolCtx, reg, call)
				if err := ctx.Err(); err != nil {
					if !hidden {
						outcome.Parts = mergeOutcomeParts(outcome.Parts, result.Parts)
						outcome.Changes = append(outcome.Changes, result.Changes...)
						updateOutcomeProgressStats(&outcome)
					}
					setOutcomeProgressStatus(&outcome, "cancelled")
					emitOutcomeProgress(sink, outcome.Parts)
					return outcome, err
				}
				guard.after(call, result, &toolMsg)
			}
			toolStatus := "completed"
			if result.IsError {
				toolStatus = "error"
			}
			if (!isProgressUpdate || result.IsError) && !hidden {
				emitChatEvent(sink, ChatStreamEvent{
					Type:       "tool",
					Message:    result.Summary,
					ToolName:   call.Function.Name,
					ToolCallID: call.ID,
					ToolInput:  toolInput,
					Status:     toolStatus,
					Parts:      result.Parts,
				})
			}
			if !hidden {
				outcome.Parts = mergeOutcomeParts(outcome.Parts, result.Parts)
				outcome.Changes = append(outcome.Changes, result.Changes...)
				updateOutcomeProgressStats(&outcome)
				emitOutcomeProgress(sink, outcome.Parts)
			}
			// 把工具结果回喂给模型。
			toolMsg.ToolError = result.IsError
			messages = append(messages, toolMsg)
			roundRecords = append(roundRecords, toolLoopRoundRecord{
				Name:      call.Function.Name,
				Arguments: append(json.RawMessage(nil), call.Function.Arguments...),
				Result:    result,
			})
			if guard.forceFinalResponse {
				toolsDisabled = true
				break
			}
		}
		if roundHasSubstantiveTool {
			emptyCompletionStage = 0
			forceSubstantiveTool = false
		} else if emptyCompletionStage > 0 && len(completion.ToolCalls) > 0 {
			// A progress update keeps the user informed but does not advance the
			// objective. Require a real tool action on the next provider round.
			forceSubstantiveTool = true
		}
		if period, stalled := guard.cycles.observe(roundRecords); stalled {
			feedback := toolLoopCycleFeedback(period)
			appendToolLoopFeedback(messages, feedback)
			toolsDisabled = true
			emitChatEvent(sink, ChatStreamEvent{
				Type:    "status",
				Message: feedback,
				Status:  "running",
			})
		}
	}
}

func pendingSubagentRecoveryMessage() protocol.Message {
	return protocol.Message{
		Role:         "user",
		InternalKind: "subagent-pending",
		Content:      "[EchoCode runtime update]\n后台子代理仍在运行。请先继续处理当前任务中与它们不重叠的独立工作；仅在需要它们的结果进行最终综合时调用 await_subagents。不要无意义地空等，也不要在未收集所需结果时仓促结束。\n[/EchoCode runtime update]",
	}
}

func isModelProgressTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "update_plan", "report_progress":
		return true
	default:
		return false
	}
}

func toolProgressMessage(name, status, output string) string {
	if message := strings.TrimSpace(output); message != "" && (status == "waiting" || status == "retrying") {
		return message
	}
	switch status {
	case "waiting":
		return fmt.Sprintf("正在等待 %s", name)
	case "retrying":
		return fmt.Sprintf("正在重试 %s", name)
	default:
		return fmt.Sprintf("正在运行 %s", name)
	}
}

func toolResultRecoveryMessage(final bool) protocol.Message {
	instruction := strings.Join([]string{
		"[EchoCode private tool-result recovery]",
		"The previous completion returned no user-facing answer after tool execution. The task is not complete.",
		"Continue the task autonomously in this same turn. If more work is needed, call a substantive tool now; report_progress or update_plan alone does not advance the objective. Do not ask the user to reply with 'continue'.",
		"Treat web_search snippets only as discovery: for named software, open and read the official website, official documentation, or real source repository before drawing conclusions.",
		"For questions about the user's computer or current configuration, inspect the authorized local state or explicitly request the missing permission. Never substitute a raw search-result list for the requested diagnosis.",
		"[/EchoCode private tool-result recovery]",
	}, "\n")
	if final {
		instruction = strings.Join([]string{
			"[EchoCode private tool-result recovery]",
			"Produce the final user-facing answer now from the verified tool evidence already in the conversation.",
			"If the evidence is insufficient because tools are unavailable or a permission/input is genuinely missing, say exactly what is blocked and what is needed. Do not ask the user to reply with 'continue' for work the available tools can perform. Include only relevant source links actually used. Never present raw search snippets as a completed answer.",
			"[/EchoCode private tool-result recovery]",
		}, "\n")
	}
	return protocol.Message{Role: "user", Content: instruction, InternalKind: toolResultRecoveryKind}
}

func isToolCompatibilityError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if info, ok := protocol.ProviderErrorDetails(err); ok {
		// Authentication, throttling, policy, and transient server failures are
		// never evidence that the endpoint lacks function calling.
		if info.HTTPStatus == 401 || info.HTTPStatus == 403 || info.HTTPStatus == 408 ||
			info.HTTPStatus == 409 || info.HTTPStatus == 429 || info.HTTPStatus >= 500 {
			return false
		}
		code := strings.ToLower(strings.TrimSpace(info.Code + " " + info.Type))
		mentionsCapability := strings.Contains(code, "tool") || strings.Contains(code, "function_call") || strings.Contains(code, "function-call")
		rejectsCapability := strings.Contains(code, "unsupported") || strings.Contains(code, "not_supported") ||
			strings.Contains(code, "unknown_parameter") || strings.Contains(code, "unrecognized_parameter")
		if mentionsCapability && rejectsCapability {
			return true
		}
		return toolCompatibilityMessage(info.Message)
	}
	return toolCompatibilityMessage(err.Error())
}

func isParallelToolCallsCompatibilityError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := err.Error()
	if info, ok := protocol.ProviderErrorDetails(err); ok {
		if info.HTTPStatus == 401 || info.HTTPStatus == 403 || info.HTTPStatus == 408 ||
			info.HTTPStatus == 409 || info.HTTPStatus == 429 || info.HTTPStatus >= 500 {
			return false
		}
		message = strings.TrimSpace(info.Code + " " + info.Type + " " + info.Message)
	}
	message = strings.ToLower(strings.TrimSpace(message))
	mentionsField := strings.Contains(message, "parallel_tool_calls") || strings.Contains(message, "parallel tool calls")
	if !mentionsField {
		return false
	}
	for _, marker := range []string{
		"unsupported", "not supported", "not_support", "unknown parameter", "unknown field",
		"unrecognized", "not allowed", "unexpected", "additional properties",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isRequiredToolChoiceCompatibilityError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := err.Error()
	if info, ok := protocol.ProviderErrorDetails(err); ok {
		if info.HTTPStatus == 401 || info.HTTPStatus == 403 || info.HTTPStatus == 408 ||
			info.HTTPStatus == 409 || info.HTTPStatus == 429 || info.HTTPStatus >= 500 {
			return false
		}
		message = strings.TrimSpace(info.Code + " " + info.Type + " " + info.Message)
	}
	message = strings.ToLower(strings.TrimSpace(message))
	if !strings.Contains(message, "tool_choice") && !strings.Contains(message, "tool choice") {
		return false
	}
	for _, marker := range []string{
		"unsupported", "not supported", "not_support", "unknown parameter", "unknown field",
		"unrecognized", "not allowed", "unexpected", "invalid value", "invalid_request",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func toolCompatibilityMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, marker := range []string{
		"does not support tool completions",
		"does not support tool calls",
		"doesn't support tool calls",
		"tool calls are not supported",
		"tool calling is not supported",
		"tool use is not supported",
		"function calling is not supported",
		"function calls are not supported",
		"unsupported parameter: tools",
		"unsupported parameter 'tools'",
		"unsupported parameter \"tools\"",
		"unknown parameter: tools",
		"unknown parameter: 'tools'",
		"unknown parameter: \"tools\"",
		"unrecognized request argument supplied: tools",
		"unknown field: tools",
		"unknown field 'tools'",
		"unknown field \"tools\"",
		"'tools' is not allowed",
		"\"tools\" is not allowed",
		"unsupported tool_choice",
		"unknown parameter: tool_choice",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isRetryablePostToolCompletionError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if info, ok := protocol.ProviderErrorDetails(err); ok {
		return info.Retryable
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"eof", "timeout", "timed out", "connection reset", "connection refused",
		"server closed", "temporarily unavailable", "bad gateway", "service unavailable",
		"gateway timeout", "deadline", "超时", "中断", "http 502", "http 503", "http 504",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func waitForPostToolRetry(ctx context.Context, retry int) error {
	timer := time.NewTimer(time.Duration(retry) * 350 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mergeOutcomeParts(existing, incoming []tools.ResultPart) []tools.ResultPart {
	for _, part := range incoming {
		if part.Kind == tools.PartToolCall && part.Name == "web_search" && part.Status != "error" {
			continue
		}
		if part.Kind == tools.PartWebSearch {
			replaced := false
			for index := range existing {
				if existing[index].Kind != tools.PartWebSearch {
					continue
				}
				existing[index] = mergeWebSearchParts(existing[index], part)
				replaced = true
				break
			}
			if !replaced {
				existing = append(existing, part)
			}
			continue
		}
		if part.Kind == tools.PartProviderNotice {
			identity := providerResultPartIdentity(part)
			duplicate := false
			for _, current := range existing {
				if current.Kind == tools.PartProviderNotice && providerResultPartIdentity(current) == identity {
					duplicate = true
					break
				}
			}
			if !duplicate {
				existing = append(existing, part)
			}
			continue
		}
		if part.Kind == tools.PartSecretResult && strings.TrimSpace(part.SecretID) != "" {
			replaced := false
			for index := range existing {
				if existing[index].Kind != tools.PartSecretResult || existing[index].SecretID != part.SecretID {
					continue
				}
				existing[index] = mergeSecretResultParts(existing[index], part)
				replaced = true
				break
			}
			if !replaced {
				existing = append(existing, part)
			}
			continue
		}
		if part.Kind == tools.PartSubagent && strings.TrimSpace(part.TaskID) != "" {
			replaced := false
			for index := range existing {
				if existing[index].Kind != tools.PartSubagent || existing[index].TaskID != part.TaskID {
					continue
				}
				existing[index] = mergeSubagentParts(existing[index], part)
				replaced = true
				break
			}
			if !replaced {
				existing = append(existing, part)
			}
			continue
		}
		if part.Kind != tools.PartProgress {
			existing = append(existing, part)
			continue
		}
		replaced := false
		for index := range existing {
			if existing[index].Kind == tools.PartProgress {
				existing[index] = part
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, part)
		}
	}
	return existing
}

func mergeSecretResultParts(existing, incoming tools.ResultPart) tools.ResultPart {
	if incoming.Status == "" {
		incoming.Status = existing.Status
	}
	if incoming.SecretLabel == "" {
		incoming.SecretLabel = existing.SecretLabel
	}
	if incoming.SecretSource == "" {
		incoming.SecretSource = existing.SecretSource
	}
	return incoming
}

func mergeSubagentParts(existing, incoming tools.ResultPart) tools.ResultPart {
	if subagentStatusIsTerminal(existing.Status) && !subagentStatusIsTerminal(incoming.Status) {
		return existing
	}
	return incoming
}

func subagentStatusIsTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "error", "cancelled":
		return true
	default:
		return false
	}
}

func providerResultPartIdentity(part tools.ResultPart) string {
	return strings.Join([]string{
		part.NoticeKind,
		part.RequestedModel,
		part.EffectiveModel,
		part.RetryModel,
		strings.Join(part.UseCases, ","),
		strings.Join(part.Reasons, ","),
		strings.Join(part.Verifications, ","),
		strings.Join(part.MetadataKeys, ","),
		part.ErrorCode,
	}, "\x00")
}

func mergeWebSearchParts(existing, incoming tools.ResultPart) tools.ResultPart {
	if existing.Query == "" {
		existing.Query = incoming.Query
	} else if incoming.Query != "" && existing.Query != incoming.Query && !strings.Contains(existing.Query, "含补充搜索") {
		existing.Query += "（含补充搜索）"
	}
	byURL := make(map[string]int, len(existing.Sources)+len(incoming.Sources))
	for index, source := range existing.Sources {
		byURL[strings.TrimSpace(source.URL)] = index
	}
	for _, source := range incoming.Sources {
		key := strings.TrimSpace(source.URL)
		if key == "" {
			continue
		}
		if index, ok := byURL[key]; ok {
			if existing.Sources[index].Title == "" {
				existing.Sources[index].Title = source.Title
			}
			if existing.Sources[index].Snippet == "" {
				existing.Sources[index].Snippet = source.Snippet
			}
			continue
		}
		if len(existing.Sources) >= 16 {
			break
		}
		byURL[key] = len(existing.Sources)
		existing.Sources = append(existing.Sources, source)
	}
	return existing
}

func updateOutcomeProgressStats(outcome *toolLoopOutcome) {
	if outcome == nil {
		return
	}
	files, additions, deletions := tools.SummarizeFileChanges(outcome.Changes)
	for index := range outcome.Parts {
		if outcome.Parts[index].Kind != tools.PartProgress {
			continue
		}
		outcome.Parts[index].ChangedFiles = files
		outcome.Parts[index].Additions = additions
		outcome.Parts[index].Deletions = deletions
		return
	}
}

func setOutcomeProgressStatus(outcome *toolLoopOutcome, status string) {
	if outcome == nil {
		return
	}
	updateOutcomeProgressStats(outcome)
	for index := range outcome.Parts {
		if outcome.Parts[index].Kind == tools.PartProgress {
			if status == "completed" {
				for _, step := range outcome.Parts[index].Steps {
					if step.Status != "completed" {
						status = "running"
						break
					}
				}
			}
			outcome.Parts[index].TaskStatus = status
			return
		}
	}
}

func emitOutcomeProgress(sink ChatEventSink, parts []tools.ResultPart) {
	for index := range parts {
		if parts[index].Kind != tools.PartProgress {
			continue
		}
		progress := parts[index]
		emitChatEvent(sink, ChatStreamEvent{Type: "progress", Progress: &progress})
		return
	}
}

// executeToolCall 执行单个工具调用，返回结构化结果与回喂给模型的 tool 消息。
func (s *Service) executeToolCall(ctx context.Context, reg *tools.Registry, call protocol.ToolCall) (result tools.Result, message protocol.Message) {
	name := call.Function.Name
	normalizedArgs := normalizeToolArgs(call.Function.Arguments)
	startedAt := time.Now()
	toolRegistered := false
	defer func() {
		result = ensureToolExecutionMetadata(result, name, call.ID, normalizedArgs, startedAt, time.Now())
		message.ToolError = result.IsError
		if toolRegistered {
			s.recordToolTerminalEvent(name, call.ID, result)
		}
	}()
	var argumentObject map[string]json.RawMessage
	if err := json.Unmarshal(normalizedArgs, &argumentObject); err != nil || argumentObject == nil {
		summary := fmt.Sprintf("工具 %s 的参数必须是完整的 JSON 对象，请修正后重试。", name)
		return tools.Result{Summary: summary, IsError: true}, protocol.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: summary, ToolError: true}
	}
	tool, ok := reg.Get(name)
	if !ok {
		summary := fmt.Sprintf("未知工具: %s", name)
		return tools.Result{
				Summary: summary,
				IsError: true,
				Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: name, Status: "error", Output: summary}},
			}, protocol.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       name,
				Content:    summary,
			}
	}
	toolRegistered = true
	s.recordToolStartedEvent(name, call.ID, normalizedArgs, startedAt)
	timeout := time.Duration(s.runtimeSettings.ToolTimeoutSeconds) * time.Second
	result, err := s.runToolWithWatchdog(ctx, tool, name, normalizedArgs, timeout)
	if err != nil {
		detail := redactSensitiveText(err.Error())
		summary := fmt.Sprintf("工具 %s 执行出错: %s", name, detail)
		if strings.TrimSpace(result.Summary) == "" {
			result.Summary = summary
		} else if !strings.Contains(result.Summary, detail) {
			result.Summary = strings.TrimSpace(result.Summary + "\n" + summary)
		}
		result.IsError = true
		for index := range result.Parts {
			part := &result.Parts[index]
			if part.Kind != tools.PartToolCall {
				continue
			}
			part.Status = "error"
			if strings.TrimSpace(part.Stderr) == "" {
				part.Stderr = detail
			}
		}
	}
	result = ensureToolErrorPart(result, name, normalizedArgs)
	if toolCallInterrupted(ctx, err) {
		result = markToolResultInterrupted(result, name, normalizedArgs)
	}
	result = applyToolResultPolicy(result, s.runtimeSettings.ToolResultPolicy, name)
	if len(result.Changes) > 0 {
		for _, change := range result.Changes {
			if snapshotErr := s.recordFileSnapshot(change); snapshotErr != nil {
				rollbackErr := tools.RestoreFile(s.sandboxPolicy(), change.Path, change.Before, change.Existed, change.LineEnding, change.Encoding, change.HadBOM)
				summary := fmt.Sprintf("工具 %s 已写入文件，但记录回退快照失败: %v", name, snapshotErr)
				if rollbackErr != nil {
					summary += fmt.Sprintf("；自动恢复也失败: %v", rollbackErr)
				} else {
					summary += "；已自动恢复写入前状态"
				}
				result = tools.Result{Summary: summary, IsError: true}
				result = ensureToolErrorPart(result, name, normalizedArgs)
				break
			}
			s.turnChanges = append(s.turnChanges, change)
		}
	}
	result = ensureToolExecutionMetadata(result, name, call.ID, normalizedArgs, startedAt, time.Now())
	var artifactRecords []ArtifactRecord
	// A tool can write a usable file and then fail a later validation step.
	// Keep explicitly declared files in the registry even for that partial
	// failure, so the model can reuse the exact path instead of rediscovering
	// it by scanning the workspace. Arbitrary failed changes are not recorded:
	// without a successful snapshot they are not safe rewind artifacts.
	if !result.IsError || toolResultDeclaresArtifacts(result) {
		// Attach the call ID before registration so the durable record can be
		// associated with the exact tool invocation even if the turn is later
		// interrupted before an assistant message is produced.
		result = ensureToolExecutionMetadata(result, name, call.ID, normalizedArgs, startedAt, time.Now())
		var artifactErr error
		artifactRecords, artifactErr = s.recordToolArtifacts(name, call.ID, result)
		if artifactErr != nil {
			result.IsError = true
			result.Summary = strings.TrimSpace(result.Summary + "\n" + artifactErr.Error())
			for index := range result.Parts {
				if result.Parts[index].Kind != tools.PartToolCall {
					continue
				}
				result.Parts[index].Status = "error"
				result.Parts[index].Output = result.Summary
			}
		}
	}
	// Build model feedback only after snapshot recording and any automatic
	// rollback, so the model never receives a stale success message.
	feedback := formatToolResultFeedback(result, name)
	if context := formatLocalArtifactContext(artifactReferencesFromRecords(artifactRecords), 4_000); context != "" {
		feedback += "\n\n" + context
	}
	return result, protocol.Message{
		Role:        "tool",
		ToolCallID:  call.ID,
		Name:        name,
		Content:     feedback,
		Attachments: protocolToolAttachments(result.Attachments),
	}
}

type toolResultFeedbackMetadata struct {
	Tool             string `json:"tool"`
	CallID           string `json:"callId,omitempty"`
	Status           string `json:"status"`
	Input            string `json:"input,omitempty"`
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	ExitCode         *int   `json:"exitCode,omitempty"`
	DurationMs       int64  `json:"durationMs,omitempty"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	Output           string `json:"output,omitempty"`
}

func formatToolResultFeedback(result tools.Result, name string) string {
	feedback := strings.TrimSpace(result.Summary)
	if feedback == "" {
		feedback = "（无输出）"
	}

	var execution tools.ResultPart
	found := false
	for _, part := range result.Parts {
		if part.Kind != tools.PartToolCall || strings.TrimSpace(part.Name) != strings.TrimSpace(name) {
			continue
		}
		if !found {
			execution = part
			found = true
			continue
		}
		execution = mergeTaskRuntimeToolPart(execution, part)
	}
	if !found {
		return feedback
	}

	status := strings.TrimSpace(execution.Status)
	if status == "" {
		if result.IsError {
			status = "error"
		} else {
			status = "ok"
		}
	}
	metadata := toolResultFeedbackMetadata{
		Tool:             strings.TrimSpace(name),
		CallID:           strings.TrimSpace(execution.ToolCallID),
		Status:           status,
		Input:            redactSensitiveText(strings.TrimSpace(execution.Input)),
		WorkingDirectory: strings.TrimSpace(execution.WorkingDirectory),
		ExitCode:         execution.ExitCode,
		DurationMs:       execution.DurationMs,
		Stdout:           redactSensitiveText(strings.TrimSpace(execution.Stdout)),
		Stderr:           redactSensitiveText(strings.TrimSpace(execution.Stderr)),
	}
	if metadata.Stdout == "" && metadata.Stderr == "" {
		output := redactSensitiveText(strings.TrimSpace(execution.Output))
		if output != feedback {
			metadata.Output = output
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return feedback
	}
	return feedback + "\n\nEchoCode tool execution metadata:\n" + string(encoded)
}

func toolResultDeclaresArtifacts(result tools.Result) bool {
	for _, part := range result.Parts {
		if part.Kind == tools.PartFile && strings.TrimSpace(part.Path) != "" {
			return true
		}
	}
	return false
}

func toolNeedsExclusiveWorkspaceAccess(name string, tool tools.Tool) bool {
	if _, ok := tool.(tools.MutatingTool); ok {
		return true
	}
	if strings.HasPrefix(name, "plugin__") {
		if readOnly, ok := tool.(interface{ ReadOnly() bool }); ok {
			return !readOnly.ReadOnly()
		}
		return true
	}
	switch name {
	case "run_command", "git", "git_repository", "terminal":
		return true
	}
	return strings.HasPrefix(name, "mcp__")
}

func ensureToolExecutionMetadata(result tools.Result, name, toolCallID string, rawArgs json.RawMessage, startedAt, completedAt time.Time) tools.Result {
	status := "ok"
	if result.IsError {
		status = "error"
	}
	input := toolInputForDisplay(name, rawArgs)
	durationMs := completedAt.Sub(startedAt).Milliseconds()
	if durationMs < 1 {
		durationMs = 1
	}
	found := false
	for index := range result.Parts {
		part := &result.Parts[index]
		if part.ToolCallID == "" {
			part.ToolCallID = toolCallID
		}
		if part.Kind != tools.PartToolCall {
			continue
		}
		found = true
		if part.Name == "" {
			part.Name = name
		}
		if part.Status == "" {
			part.Status = status
		}
		if part.Input == "" {
			part.Input = input
		}
		if part.StartedAt == "" {
			part.StartedAt = startedAt.Format(time.RFC3339Nano)
		}
		if part.CompletedAt == "" {
			part.CompletedAt = completedAt.Format(time.RFC3339Nano)
		}
		if part.DurationMs <= 0 {
			part.DurationMs = durationMs
		}
	}
	if name == (ReportProgressTool{}).Name() && !result.IsError {
		return result
	}
	if found {
		return result
	}
	result.Parts = append([]tools.ResultPart{{
		Kind:        tools.PartToolCall,
		Name:        name,
		ToolCallID:  toolCallID,
		Status:      status,
		Input:       input,
		Output:      result.Summary,
		StartedAt:   startedAt.Format(time.RFC3339Nano),
		CompletedAt: completedAt.Format(time.RFC3339Nano),
		DurationMs:  durationMs,
	}}, result.Parts...)
	return result
}

func protocolToolAttachments(attachments []tools.Attachment) []protocol.Attachment {
	converted := make([]protocol.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		converted = append(converted, protocol.Attachment{Name: attachment.Name, MIMEType: attachment.MIMEType, Data: attachment.Data})
	}
	return converted
}

func applyToolResultPolicy(result tools.Result, policy string, toolNames ...string) tools.Result {
	summaryLimit, detailLimit := 4_000, 8_000
	switch policy {
	case "balanced":
		summaryLimit, detailLimit = 12_000, 24_000
	case "raw-local":
		summaryLimit, detailLimit = 32_000, 64_000
	}
	if len(toolNames) > 0 && toolNames[0] == (LoadSkillTool{}).Name() {
		summaryLimit = 64_000
	}
	result.Summary = clipContextText(result.Summary, summaryLimit)
	for index := range result.Parts {
		part := &result.Parts[index]
		part.Input = clipContextText(part.Input, detailLimit/2)
		part.Output = clipContextText(part.Output, detailLimit)
		part.Stdout = clipContextText(part.Stdout, detailLimit)
		part.Stderr = clipContextText(part.Stderr, detailLimit)
		part.Summary = clipContextText(part.Summary, detailLimit)
		part.CurrentAction = clipContextText(part.CurrentAction, detailLimit/2)
		if part.Kind == tools.PartText {
			part.Text = clipContextText(part.Text, detailLimit)
		}
		for sourceIndex := range part.Sources {
			part.Sources[sourceIndex].Snippet = clipContextText(part.Sources[sourceIndex].Snippet, 800)
		}
	}
	return result
}

func ensureToolErrorPart(result tools.Result, name string, rawArgs json.RawMessage) tools.Result {
	if !result.IsError {
		return result
	}
	for _, part := range result.Parts {
		if part.Kind == tools.PartToolCall {
			return result
		}
	}
	errorPart := tools.ResultPart{
		Kind:   tools.PartToolCall,
		Name:   name,
		Status: "error",
		Input:  toolInputForDisplay(name, rawArgs),
		Output: result.Summary,
	}
	// A tool error does not invalidate evidence it already produced. Keep file,
	// diff, text, web, image, and subagent parts so the model can diagnose the
	// failure or continue from a usable partial result.
	result.Parts = append([]tools.ResultPart{errorPart}, result.Parts...)
	return result
}

func toolCallInterrupted(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))
}

func markToolResultInterrupted(result tools.Result, name string, rawArgs json.RawMessage) tools.Result {
	summary := "工具已因任务停止而中断，迟到结果已被忽略。"
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = summary
	} else if !strings.Contains(result.Summary, summary) {
		result.Summary = strings.TrimSpace(result.Summary + "\n" + summary)
	}
	result.IsError = true
	found := false
	for index := range result.Parts {
		part := &result.Parts[index]
		if part.Kind != tools.PartToolCall {
			continue
		}
		found = true
		part.Status = "cancelled"
		if part.Input == "" {
			part.Input = toolInputForDisplay(name, rawArgs)
		}
		if part.Output == "" {
			part.Output = summary
		}
		if part.Stderr == "" {
			part.Stderr = summary
		}
	}
	if !found {
		result.Parts = append(result.Parts, tools.ResultPart{
			Kind: tools.PartToolCall, Name: name, Status: "cancelled",
			Input: toolInputForDisplay(name, rawArgs), Output: summary, Stderr: summary,
		})
	}
	return result
}

func toolInputForDisplay(name string, rawArgs json.RawMessage) string {
	rawArgs = normalizeToolArgs(rawArgs)
	var args map[string]any
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ""
	}
	key := "path"
	switch name {
	case "decode_protected_secret":
		// The encoded value is still sensitive input. Only expose the safe-card
		// action in approvals, progress events, and the durable timeline.
		return "已接收编码值，正在保存为安全卡片"
	case "load_skill":
		key = "name"
	case "search", "web_search":
		key = "query"
	case "read_repository", "read_webpage":
		key = "url"
	case "run_command":
		return tools.RunCommandInputForDisplay(rawArgs)
	case "download_file":
		return tools.DownloadFileInputForDisplay(rawArgs)
	case "git_repository":
		return tools.GitRepositoryInputForDisplay(rawArgs)
	case "terminal":
		command, _ := args["command"].(string)
		action, _ := args["action"].(string)
		sessionID, _ := args["session_id"].(string)
		return terminalActionDisplay(action, sessionID, command)
	case "ssh":
		return sshToolInputForDisplay(rawArgs)
	case "copy_file":
		source, _ := args["source"].(string)
		destination, _ := args["destination"].(string)
		return strings.TrimSpace(source + " -> " + destination)
	case "browser":
		if value, _ := args["url"].(string); strings.TrimSpace(value) != "" {
			return tools.SafeDownloadURLForDisplay(value)
		}
		key = "action"
	case "computer":
		action, _ := args["action"].(string)
		windowID, _ := args["window_id"].(string)
		return strings.TrimSpace(action + " " + windowID)
	default:
		if strings.Contains(strings.ToLower(name), "__codegraph_") {
			for _, candidate := range []string{"query", "symbol", "file", "path", "pattern"} {
				if value, _ := args[candidate].(string); strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
			if strings.HasSuffix(strings.ToLower(name), "__codegraph_status") {
				return "检查代码索引状态"
			}
		}
	}
	value, _ := args[key].(string)
	return value
}
