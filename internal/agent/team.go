package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/cache"
	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	TeamRolePlanner     = "planner"
	TeamRoleImplementer = "implementer"
	TeamRoleTester      = "tester"
	TeamRoleReviewer    = "reviewer"
	TeamRoleSynthesizer = "synthesizer"
)

var teamRoleOrder = []string{
	TeamRolePlanner,
	TeamRoleImplementer,
	TeamRoleTester,
	TeamRoleReviewer,
	TeamRoleSynthesizer,
}

type TeamState struct {
	Enabled     bool            `json:"enabled"`
	Active      bool            `json:"active"`
	RunID       string          `json:"runId,omitempty"`
	Status      string          `json:"status"`
	CurrentRole string          `json:"currentRole,omitempty"`
	Roles       []TeamRoleState `json:"roles"`
	StartedAt   string          `json:"startedAt,omitempty"`
	CompletedAt string          `json:"completedAt,omitempty"`
	Summary     string          `json:"summary,omitempty"`
}

type TeamRoleState struct {
	Role       string             `json:"role"`
	Label      string             `json:"label"`
	Enabled    bool               `json:"enabled"`
	Status     string             `json:"status"`
	ProviderID string             `json:"providerId,omitempty"`
	Model      string             `json:"model,omitempty"`
	Attempt    int                `json:"attempt"`
	Verdict    string             `json:"verdict,omitempty"`
	Summary    string             `json:"summary,omitempty"`
	Error      string             `json:"error,omitempty"`
	Usage      cache.UsageMetrics `json:"usage"`
	StartedAt  string             `json:"startedAt,omitempty"`
	FinishedAt string             `json:"finishedAt,omitempty"`
}

type TeamRoleEvent struct {
	RunID      string             `json:"runId"`
	Role       string             `json:"role"`
	Label      string             `json:"label"`
	Status     string             `json:"status"`
	ProviderID string             `json:"providerId,omitempty"`
	Model      string             `json:"model,omitempty"`
	Attempt    int                `json:"attempt"`
	Verdict    string             `json:"verdict,omitempty"`
	Summary    string             `json:"summary,omitempty"`
	Error      string             `json:"error,omitempty"`
	Usage      cache.UsageMetrics `json:"usage"`
}

type teamArtifact struct {
	role    string
	attempt int
	content string
	verdict string
	route   chatRoute
	parts   []tools.ResultPart
	usage   cache.UsageMetrics
}

const teamRunCheckpointVersion = 1

type teamRunCheckpoint struct {
	Version      int                      `json:"version"`
	Status       string                   `json:"status"`
	NextRole     string                   `json:"nextRole"`
	NextAttempt  int                      `json:"nextAttempt"`
	ReviewRound  int                      `json:"reviewRound"`
	PlanStarted  bool                     `json:"planStarted"`
	PlanApproved bool                     `json:"planApproved"`
	Team         TeamState                `json:"team"`
	Artifacts    []teamCheckpointArtifact `json:"artifacts"`
	Parts        []tools.ResultPart       `json:"parts"`
	Usage        cache.UsageMetrics       `json:"usage"`
	UpdatedAt    string                   `json:"updatedAt"`
}

type teamCheckpointArtifact struct {
	Role       string             `json:"role"`
	Attempt    int                `json:"attempt"`
	Content    string             `json:"content"`
	Verdict    string             `json:"verdict,omitempty"`
	ProviderID string             `json:"providerId,omitempty"`
	Model      string             `json:"model,omitempty"`
	Parts      []tools.ResultPart `json:"parts,omitempty"`
	Usage      cache.UsageMetrics `json:"usage"`
}

var errTeamRunPaused = errors.New("AI team run paused")

func isTeamRole(role string) bool {
	for _, candidate := range teamRoleOrder {
		if role == candidate {
			return true
		}
	}
	return false
}

func teamRoleLabel(role string) string {
	role = strings.TrimSpace(role)
	switch role {
	case TeamRolePlanner:
		return "规划"
	case TeamRoleImplementer:
		return "实现"
	case TeamRoleTester:
		return "测试"
	case TeamRoleReviewer:
		return "审阅"
	case TeamRoleSynthesizer:
		return "汇总"
	default:
		if role == "" {
			return "协作任务"
		}
		return "协作任务（" + role + "）"
	}
}

func cloneTeamState(state TeamState) TeamState {
	state.Roles = append([]TeamRoleState(nil), state.Roles...)
	return state
}

func cloneTeamRunCheckpoint(checkpoint *teamRunCheckpoint) *teamRunCheckpoint {
	if checkpoint == nil {
		return nil
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		clone := *checkpoint
		clone.Team = cloneTeamState(checkpoint.Team)
		clone.Artifacts = append([]teamCheckpointArtifact(nil), checkpoint.Artifacts...)
		clone.Parts = append([]tools.ResultPart(nil), checkpoint.Parts...)
		return &clone
	}
	var clone teamRunCheckpoint
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil
	}
	return &clone
}

func newTeamRunCheckpoint(settings TeamSettings) *teamRunCheckpoint {
	nextRole := firstTeamWorkStage(settings)
	return &teamRunCheckpoint{
		Version:     teamRunCheckpointVersion,
		Status:      "running",
		NextRole:    nextRole,
		NextAttempt: 1,
		Artifacts:   []teamCheckpointArtifact{},
		Parts:       []tools.ResultPart{},
	}
}

func teamSettingsForPolicy(settings TeamSettings, policy tools.SandboxPolicy) TeamSettings {
	settings.Roles = append([]TeamRoleSetting(nil), settings.Roles...)
	readOnly := strings.EqualFold(strings.TrimSpace(policy.FilesystemAccess), "read-only") ||
		strings.EqualFold(strings.TrimSpace(policy.SandboxMode), "read-only")
	if !readOnly {
		return settings
	}
	for index := range settings.Roles {
		if settings.Roles[index].Role == TeamRoleImplementer {
			settings.Roles[index].Enabled = false
		}
	}
	return settings
}

func (s *Service) persistTeamRunCheckpoint(checkpoint *teamRunCheckpoint) error {
	if checkpoint == nil {
		s.teamResume = nil
		return nil
	}
	checkpoint.Version = teamRunCheckpointVersion
	checkpoint.Team = cloneTeamState(s.teamState)
	checkpoint.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	if checkpoint.Status == "running" || checkpoint.Status == "paused" {
		s.teamResume = cloneTeamRunCheckpoint(checkpoint)
	} else {
		s.teamResume = nil
	}
	if s.eventStore == nil {
		return nil
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode AI team checkpoint: %w", err)
	}
	hash, err := s.eventStore.WriteSnapshot(string(encoded))
	if err != nil {
		return fmt.Errorf("store AI team checkpoint: %w", err)
	}
	if _, err := s.appendEvent(eventlog.EventPayload{TeamCheckpointHash: hash}, eventlog.EventTeamCheckpoint); err != nil {
		return fmt.Errorf("append AI team checkpoint: %w", err)
	}
	return nil
}

func (s *Service) restoreTeamRunCheckpoint(hash string) *teamRunCheckpoint {
	if s.eventStore == nil || strings.TrimSpace(hash) == "" {
		return nil
	}
	encoded, err := s.eventStore.ReadSnapshot(hash)
	if err != nil {
		return nil
	}
	var checkpoint teamRunCheckpoint
	if err := json.Unmarshal([]byte(encoded), &checkpoint); err != nil || checkpoint.Version != teamRunCheckpointVersion {
		return nil
	}
	if checkpoint.NextAttempt < 1 {
		checkpoint.NextAttempt = 1
	}
	return &checkpoint
}

func teamArtifactsFromCheckpoint(checkpoint *teamRunCheckpoint) []teamArtifact {
	if checkpoint == nil {
		return nil
	}
	artifacts := make([]teamArtifact, 0, len(checkpoint.Artifacts))
	for _, item := range checkpoint.Artifacts {
		artifacts = append(artifacts, teamArtifact{
			role: item.Role, attempt: item.Attempt, content: item.Content, verdict: item.Verdict,
			route: chatRoute{Provider: ModelProviderSetting{ID: item.ProviderID}, ModelID: item.Model},
			parts: append([]tools.ResultPart(nil), item.Parts...), usage: item.Usage,
		})
	}
	return artifacts
}

func checkpointTeamArtifacts(artifacts []teamArtifact) []teamCheckpointArtifact {
	checkpoint := make([]teamCheckpointArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		checkpoint = append(checkpoint, teamCheckpointArtifact{
			Role: artifact.role, Attempt: artifact.attempt, Content: artifact.content, Verdict: artifact.verdict,
			ProviderID: artifact.route.Provider.ID, Model: artifact.route.ModelID,
			Parts: append([]tools.ResultPart(nil), artifact.parts...), Usage: artifact.usage,
		})
	}
	return checkpoint
}

func teamArtifactAt(artifacts []teamArtifact, role string, attempt int) (teamArtifact, bool) {
	for index := len(artifacts) - 1; index >= 0; index-- {
		if artifacts[index].role == role && artifacts[index].attempt == attempt {
			return artifacts[index], true
		}
	}
	return teamArtifact{}, false
}

type teamResumeTurnKey struct{}

func withTeamResumeTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, teamResumeTurnKey{}, true)
}

func isTeamResumeTurn(ctx context.Context) bool {
	resume, _ := ctx.Value(teamResumeTurnKey{}).(bool)
	return resume
}

func (s *Service) hasPausedTeamRun() bool {
	if s == nil {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.hasPausedTeamRunLocked()
}

// hasPausedTeamRunLocked is for state-building paths that already hold
// stateMu. Keeping the lock-taking wrapper separate prevents a workbench
// snapshot from trying to recursively acquire Go's non-reentrant RWMutex.
func (s *Service) hasPausedTeamRunLocked() bool {
	return s != nil && s.teamResume != nil && strings.EqualFold(strings.TrimSpace(s.teamResume.Status), "paused")
}

// AbandonPausedTeamTask explicitly ends a paused team run. It preserves the
// append-only checkpoint for audit and rewind, but removes it from the active
// resume slot so a later user message is never interpreted as an implicit
// lifecycle decision.
func (s *Service) AbandonPausedTeamTask() (WorkbenchState, error) {
	release, err := s.beginActivity("ending a paused AI team task")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()

	checkpoint := cloneTeamRunCheckpoint(s.teamResume)
	if checkpoint == nil || !strings.EqualFold(strings.TrimSpace(checkpoint.Status), "paused") {
		return s.workbenchStateLocked(), errors.New("当前会话没有可结束的 AI 团队任务")
	}
	if checkpoint.PlanStarted && len(s.planState.Steps) > 0 && s.planState.Status != "completed" && s.planState.Status != "cancelled" {
		if err := s.finishPlanState("cancelled"); err != nil {
			return s.workbenchStateLocked(), fmt.Errorf("结束团队计划失败: %w", err)
		}
	}

	state := cloneTeamState(checkpoint.Team)
	if len(state.Roles) == 0 {
		state = cloneTeamState(s.teamState)
	}
	state.Enabled = s.runtimeSettings.Team.Enabled
	state.Active = false
	state.Status = "cancelled"
	state.CurrentRole = ""
	state.CompletedAt = time.Now().Format(time.RFC3339Nano)
	state.Summary = "用户结束了暂停的 AI 团队任务。"
	for index := range state.Roles {
		if state.Roles[index].Status == "paused" || state.Roles[index].Status == "running" || state.Roles[index].Status == "pending" {
			state.Roles[index].Status = "cancelled"
			state.Roles[index].FinishedAt = state.CompletedAt
		}
	}
	s.teamState = state
	checkpoint.Status = "abandoned"
	checkpoint.Team = cloneTeamState(state)
	checkpoint.NextRole = ""
	checkpoint.NextAttempt = 0
	if err := s.persistTeamRunCheckpoint(checkpoint); err != nil {
		return s.workbenchStateLocked(), fmt.Errorf("保存团队结束状态失败: %w", err)
	}
	return s.workbenchStateLocked(), nil
}

func (s *Service) recordTeamPauseMessage(content, model string, parts []tools.ResultPart, durations ...int64) {
	if s.eventStore == nil {
		return
	}
	durationMs := int64(0)
	if len(durations) > 0 && durations[0] > 0 {
		durationMs = durations[0]
	}
	_, _ = s.appendEvent(eventlog.EventPayload{
		Role: "assistant", Content: content, Model: model, DurationMs: durationMs, Parts: toEventParts(parts),
	}, eventlog.EventAssistantMessage)
}

func (s *Service) teamModeEnabled() bool {
	return s.runtimeSettings.Team.Enabled
}

func (s *Service) runTeamRole(
	ctx context.Context,
	settings TeamRoleSetting,
	attempt int,
	primary chatRoute,
	baseRequest protocol.ChatRequest,
	artifacts []teamArtifact,
	sink ChatEventSink,
) (teamArtifact, error) {
	role := settings.Role
	route, err := s.resolveTeamRoleRoute(settings, primary)
	if err != nil {
		s.failTeamRole(role, attempt, err, sink)
		return teamArtifact{role: role}, err
	}
	provider, err := s.chatProviderForRoute(route)
	if err != nil {
		s.failTeamRole(role, attempt, err, sink)
		return teamArtifact{role: role, route: route}, err
	}
	s.startTeamRole(role, attempt, route, sink)

	request := teamRoleRequest(baseRequest, role, attempt, route, artifacts)
	roleSink := teamRoleToolSink(sink)
	var outcome toolLoopOutcome
	roleUsage := cache.UsageMetrics{}
	observeUsage := func(usage *protocol.TokenUsage) {
		metrics := s.recordLiveUsage(usage, route, sink)
		roleUsage = addUsageMetrics(roleUsage, metrics)
	}
	if role == TeamRoleSynthesizer {
		completion, completionErr := collectProviderStream(ctx, provider, request, sink)
		if completion.Usage != nil {
			observeUsage(completion.Usage)
		}
		if completionErr != nil {
			err = completionErr
		} else {
			outcome = toolLoopOutcome{Content: completion.Content, Reasoning: completion.Reasoning, Usage: completion.Usage, Parts: providerNoticeParts(completion.Notices)}
		}
	} else {
		registry := s.buildReadOnlyRegistryForContext(ctx)
		if role == TeamRoleImplementer {
			registry = s.buildWorkerToolRegistryForContext(ctx)
		}
		outcome, err = s.runStreamingToolLoopWithState(ctx, provider, registry, request, roleSink, observeUsage)
	}

	artifact := teamArtifact{role: role, attempt: attempt, content: strings.TrimSpace(outcome.Content), route: route, parts: outcome.Parts, usage: roleUsage}
	artifact.verdict = teamVerdict(role, artifact.content)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			s.cancelTeamRole(role, attempt, route, artifact, sink)
			if ctx.Err() != nil {
				return artifact, ctx.Err()
			}
			return artifact, err
		}
		s.failTeamRoleWithArtifact(role, attempt, route, artifact, err, sink)
		return artifact, err
	}
	s.completeTeamRole(role, attempt, route, artifact, sink)
	return artifact, nil
}

func (s *Service) resolveTeamRoleRoute(settings TeamRoleSetting, primary chatRoute) (chatRoute, error) {
	if strings.TrimSpace(settings.ProviderID) == "" {
		route := primary
		if strings.TrimSpace(settings.ModelID) != "" {
			route.ModelID = strings.TrimSpace(settings.ModelID)
		}
		return route, nil
	}
	runtime := s.stateRuntimeSettings()
	provider, _, ok := findModelProvider(runtime.Model.Providers, settings.ProviderID)
	if !ok || !provider.Enabled {
		return chatRoute{}, fmt.Errorf("团队角色 %s 的供应商不可用：%s", teamRoleLabel(settings.Role), settings.ProviderID)
	}
	route, err := s.chatRouteForProvider(runtime, provider)
	if err != nil {
		return chatRoute{}, err
	}
	if strings.TrimSpace(settings.ModelID) != "" {
		route.ModelID = strings.TrimSpace(settings.ModelID)
	}
	return route, nil
}

func teamRoleRequest(base protocol.ChatRequest, role string, attempt int, route chatRoute, artifacts []teamArtifact) protocol.ChatRequest {
	request := base
	applyRouteToChatRequest(&request, route)
	request.Metadata = make(map[string]string, len(base.Metadata)+3)
	for key, value := range base.Metadata {
		request.Metadata[key] = value
	}
	request.Metadata["request_kind"] = "team_role"
	request.Metadata["team_role"] = role
	request.Metadata["team_attempt"] = fmt.Sprintf("%d", attempt)
	if threadID := strings.TrimSuffix(strings.TrimSpace(base.ThreadID), ":"); threadID != "" {
		request.ThreadID = threadID + ":team:" + role
	}
	if turnID := strings.TrimSuffix(strings.TrimSpace(base.TurnID), ":"); turnID != "" {
		request.TurnID = turnID + ":" + role + ":" + fmt.Sprintf("%d", attempt)
	}
	request.Messages = append([]protocol.Message(nil), base.Messages...)
	request.Messages = append(request.Messages, protocol.Message{Role: "user", Content: teamRoleInstruction(role, attempt, artifacts)})
	request.Tools = nil
	request.ParallelToolCalls = role != TeamRoleSynthesizer
	return request
}

func teamRoleInstruction(role string, attempt int, artifacts []teamArtifact) string {
	handoff := formatTeamHandoff(artifacts)
	var instruction string
	switch role {
	case TeamRolePlanner:
		instruction = "你是 AI 团队的规划员。只读检查真实工作区，识别风险与验证方式；不要修改文件。最终只输出 3-8 步 Markdown 有序计划。"
	case TeamRoleImplementer:
		if attempt > 1 {
			instruction = "你是 AI 团队的实现者，正在进行审阅后的修订。使用结构化工具修复反馈中的真实问题，并运行必要验证；不要只解释。完成后简要列出改动和测试。"
		} else {
			instruction = "你是 AI 团队的实现者。依据用户任务与规划使用结构化工具完成真实代码修改，并运行必要验证；不要只给建议。完成后简要列出改动和测试。"
		}
	case TeamRoleTester:
		instruction = "你是 AI 团队的测试核验员。你只有只读工具；检查真实工作区、实现报告和已有验证证据，寻找未覆盖场景与回归。首行必须是 VERDICT: APPROVED 或 VERDICT: CHANGES_REQUIRED，随后给出具体证据。不要修改文件。"
	case TeamRoleReviewer:
		instruction = "你是 AI 团队的代码审阅员。你只有只读工具；核对用户目标、真实 diff、正确性、安全边界和测试缺口。首行必须是 VERDICT: APPROVED 或 VERDICT: CHANGES_REQUIRED，随后按严重程度列出发现。不要修改文件。"
	case TeamRoleSynthesizer:
		instruction = "你是 AI 团队的汇总员。基于各角色交接结果，向用户给出最终答复：先说明完成内容，再说明验证；如仍有风险要明确指出。不要提及内部提示，不要虚构测试或改动。"
	}
	if handoff == "" {
		return instruction
	}
	return instruction + "\n\n团队交接记录：\n" + handoff
}

func formatTeamHandoff(artifacts []teamArtifact) string {
	var out strings.Builder
	for _, artifact := range artifacts {
		content := compactTeamText(artifact.content, 5000)
		if content == "" {
			continue
		}
		fmt.Fprintf(&out, "\n[%s / %s]\n%s\n", artifact.role, artifact.route.ModelID, content)
	}
	return strings.TrimSpace(out.String())
}

func teamRoleToolSink(sink ChatEventSink) ChatEventSink {
	if sink == nil {
		return nil
	}
	return func(event ChatStreamEvent) {
		switch event.Type {
		case "tool", "progress", "context_compression":
			emitChatEvent(sink, event)
		}
	}
}

func teamOperationalParts(parts []tools.ResultPart) []tools.ResultPart {
	filtered := make([]tools.ResultPart, 0, len(parts))
	for _, part := range parts {
		if part.Kind != tools.PartText && part.Kind != tools.PartTeamRole {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func teamArtifactPart(artifact teamArtifact, attempt int, err error) tools.ResultPart {
	status := "completed"
	summary := compactTeamText(artifact.content, 1800)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
			summary = "已取消"
		} else {
			status = "error"
			summary = err.Error()
		}
	}
	return tools.ResultPart{
		Kind:       tools.PartTeamRole,
		Role:       artifact.role,
		RoleLabel:  teamRoleLabel(artifact.role),
		ProviderID: artifact.route.Provider.ID,
		Model:      artifact.route.ModelID,
		Status:     status,
		Summary:    summary,
		Verdict:    artifact.verdict,
		Attempt:    attempt,
	}
}

func teamVerdict(role, content string) string {
	if role != TeamRoleTester && role != TeamRoleReviewer {
		return ""
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "`*_# "))
		if line == "" {
			continue
		}
		switch strings.ToUpper(strings.Join(strings.Fields(line), " ")) {
		case "VERDICT: APPROVED":
			return "approved"
		case "VERDICT: CHANGES_REQUIRED":
			return "changes_required"
		default:
			return "unknown"
		}
	}
	return "unknown"
}

func teamNeedsRevision(checks map[string]teamArtifact) bool {
	for _, artifact := range checks {
		if artifact.verdict == "changes_required" {
			return true
		}
	}
	return false
}

func reviewArtifacts(checks map[string]teamArtifact) string {
	var out strings.Builder
	for _, role := range []string{TeamRoleTester, TeamRoleReviewer} {
		artifact, ok := checks[role]
		if !ok || artifact.verdict != "changes_required" {
			continue
		}
		fmt.Fprintf(&out, "[%s]\n%s\n", teamRoleLabel(role), compactTeamText(artifact.content, 4000))
	}
	return strings.TrimSpace(out.String())
}

func teamFailureEvidenceContent(artifacts []teamArtifact, heading string) string {
	var out strings.Builder
	heading = strings.TrimSpace(heading)
	if heading == "" {
		heading = "以下是 AI 团队已保留的角色结果。"
	}
	out.WriteString(heading)
	for _, role := range teamRoleOrder {
		artifact, ok := latestTeamArtifact(artifacts, role)
		if !ok {
			continue
		}
		summary := compactTeamText(artifact.content, 1_800)
		if summary == "" {
			continue
		}
		fmt.Fprintf(&out, "\n\n%s结果：\n%s", teamRoleLabel(role), summary)
	}
	return out.String()
}

func compactTeamText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func addUsageMetrics(left, right cache.UsageMetrics) cache.UsageMetrics {
	left.PromptCacheHitTokens += right.PromptCacheHitTokens
	left.PromptCacheMissTokens += right.PromptCacheMissTokens
	left.InputTokens += right.InputTokens
	left.OutputTokens += right.OutputTokens
	left.EffectiveCost += right.EffectiveCost
	return left
}

func teamRoleSettings(settings TeamSettings, role string) TeamRoleSetting {
	for _, item := range settings.Roles {
		if item.Role == role {
			return item
		}
	}
	return TeamRoleSetting{Role: role, Enabled: role == TeamRoleImplementer || role == TeamRoleSynthesizer}
}

func roleEnabled(settings TeamSettings, role string) bool {
	return teamRoleSettings(settings, role).Enabled
}

func (s *Service) startTeamRun(settings TeamSettings, sink ChatEventSink) {
	roles := make([]TeamRoleState, 0, len(teamRoleOrder))
	for _, role := range teamRoleOrder {
		configured := teamRoleSettings(settings, role)
		status := "pending"
		if !configured.Enabled {
			status = "skipped"
		}
		roles = append(roles, TeamRoleState{
			Role: role, Label: teamRoleLabel(role), Enabled: configured.Enabled, Status: status,
			ProviderID: configured.ProviderID, Model: configured.ModelID,
		})
	}
	s.teamState = TeamState{
		Enabled: true, Active: true, RunID: fmt.Sprintf("team-%d", time.Now().UnixNano()),
		Status: "running", Roles: roles, StartedAt: time.Now().Format(time.RFC3339Nano),
	}
	emitChatEvent(sink, ChatStreamEvent{Type: "status", Message: "AI 团队正在分配角色"})
}

func (s *Service) startTeamRole(role string, attempt int, route chatRoute, sink ChatEventSink) {
	now := time.Now().Format(time.RFC3339Nano)
	state := s.teamRoleState(role)
	state.Status = "running"
	state.ProviderID = route.Provider.ID
	state.Model = route.ModelID
	state.Attempt = attempt
	state.Verdict = ""
	state.Summary = ""
	state.Error = ""
	state.StartedAt = now
	state.FinishedAt = ""
	s.setTeamRoleState(state)
	s.teamState.CurrentRole = role
	emitChatEvent(sink, ChatStreamEvent{Type: "team", Message: fmt.Sprintf("%s角色正在工作", state.Label), Model: route.ModelID, Team: teamEvent(s.teamState.RunID, state)})
}

func (s *Service) completeTeamRole(role string, attempt int, route chatRoute, artifact teamArtifact, sink ChatEventSink) {
	state := s.teamRoleState(role)
	state.Status = "completed"
	state.ProviderID = route.Provider.ID
	state.Model = route.ModelID
	state.Attempt = attempt
	state.Verdict = artifact.verdict
	state.Summary = compactTeamText(artifact.content, 1200)
	state.Error = ""
	state.Usage = artifact.usage
	state.FinishedAt = time.Now().Format(time.RFC3339Nano)
	s.setTeamRoleState(state)
	emitChatEvent(sink, ChatStreamEvent{Type: "team", Message: fmt.Sprintf("%s角色已完成", state.Label), Model: route.ModelID, Team: teamEvent(s.teamState.RunID, state)})
}

func (s *Service) cancelTeamRole(role string, attempt int, route chatRoute, artifact teamArtifact, sink ChatEventSink) {
	state := s.teamRoleState(role)
	state.Status = "cancelled"
	state.ProviderID = route.Provider.ID
	state.Model = route.ModelID
	state.Attempt = attempt
	state.Verdict = artifact.verdict
	state.Summary = compactTeamText(artifact.content, 1200)
	state.Error = ""
	state.Usage = artifact.usage
	state.FinishedAt = time.Now().Format(time.RFC3339Nano)
	s.setTeamRoleState(state)
	emitChatEvent(sink, ChatStreamEvent{Type: "team", Message: fmt.Sprintf("%s角色已取消", state.Label), Model: route.ModelID, Team: teamEvent(s.teamState.RunID, state)})
}

func (s *Service) failTeamRole(role string, attempt int, err error, sink ChatEventSink) {
	s.failTeamRoleWithArtifact(role, attempt, chatRoute{}, teamArtifact{role: role}, err, sink)
}

func (s *Service) failTeamRoleWithArtifact(role string, attempt int, route chatRoute, artifact teamArtifact, err error, sink ChatEventSink) {
	state := s.teamRoleState(role)
	state.Status = "error"
	state.Attempt = attempt
	state.ProviderID = route.Provider.ID
	state.Model = route.ModelID
	state.Verdict = artifact.verdict
	state.Summary = compactTeamText(artifact.content, 1200)
	state.Error = err.Error()
	state.Usage = artifact.usage
	state.FinishedAt = time.Now().Format(time.RFC3339Nano)
	s.setTeamRoleState(state)
	emitChatEvent(sink, ChatStreamEvent{Type: "team", Message: fmt.Sprintf("%s角色失败", state.Label), Model: route.ModelID, Team: teamEvent(s.teamState.RunID, state)})
}

func (s *Service) finishTeamRun(status, summary string, sink ChatEventSink) {
	s.teamState.Active = false
	s.teamState.Status = status
	s.teamState.CurrentRole = ""
	s.teamState.CompletedAt = time.Now().Format(time.RFC3339Nano)
	s.teamState.Summary = compactTeamText(summary, 1600)
	emitChatEvent(sink, ChatStreamEvent{Type: "team", Message: "AI 团队任务" + teamRunStatusLabel(status)})
}

func (s *Service) teamRoleState(role string) TeamRoleState {
	for _, state := range s.teamState.Roles {
		if state.Role == role {
			return state
		}
	}
	return TeamRoleState{Role: role, Label: teamRoleLabel(role), Enabled: true}
}

func (s *Service) setTeamRoleState(next TeamRoleState) {
	for index := range s.teamState.Roles {
		if s.teamState.Roles[index].Role == next.Role {
			s.teamState.Roles[index] = next
			return
		}
	}
	s.teamState.Roles = append(s.teamState.Roles, next)
}

func teamEvent(runID string, state TeamRoleState) *TeamRoleEvent {
	return &TeamRoleEvent{
		RunID: runID, Role: state.Role, Label: state.Label, Status: state.Status,
		ProviderID: state.ProviderID, Model: state.Model, Attempt: state.Attempt,
		Verdict: state.Verdict, Summary: state.Summary, Error: state.Error, Usage: state.Usage,
	}
}

func teamRunStatusLabel(status string) string {
	switch status {
	case "completed":
		return "已完成"
	case "cancelled":
		return "已取消"
	default:
		return "失败"
	}
}

var errTeamModeRequiresPlanner = errors.New("AI 团队模式需要高或超高推理强度")
