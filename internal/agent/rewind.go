package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/project"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// initEventStore 初始化项目清单与活动会话的事件日志。
// 若未配置 SessionsDir 则禁用持久化（eventStore=nil）。
func (s *Service) initEventStore() {
	if s.config.SessionsDir == "" {
		return
	}
	// 初始化项目清单（多项目/多会话元数据）。
	if s.config.ProjectsPath != "" {
		if store, err := project.Open(s.config.ProjectsPath); err == nil {
			s.projects = store
			_, _ = s.projects.EnsureBootstrap(s.runtimeSettings.WorkspaceRoot, defaultProjectName(s.runtimeSettings.WorkspaceRoot))
			s.pruneGeneratedBootstrapProjects()
		}
	}
	// 打开活动会话的事件日志。
	s.openActiveSession()
	s.rebuildSessionFromEvents()
	s.refreshProjectMemory()
}

func (s *Service) pruneGeneratedBootstrapProjects() {
	if s.projects == nil {
		return
	}
	removed, err := s.projects.PruneGeneratedBootstrapProjects()
	if err != nil || strings.TrimSpace(s.config.SessionsDir) == "" {
		return
	}
	for _, project := range removed {
		projectDir := ""
		for _, session := range project.Sessions {
			dir, pathErr := safeSessionEventDir(s.config.SessionsDir, project.ID, session.ID)
			if pathErr == nil {
				_ = os.RemoveAll(dir)
				projectDir = filepath.Dir(dir)
			}
		}
		if projectDir != "" {
			_ = os.Remove(projectDir)
		}
	}
}

// openActiveSession 依据项目清单的活动指针打开对应事件日志目录。
// 无项目清单时回退单会话 "default"（向后兼容）。
func (s *Service) openActiveSession() {
	projectID, sessionID := "default", "default"
	if s.projects != nil {
		pid, sid := s.projects.ActiveIDs()
		if pid != "" && sid != "" {
			projectID, sessionID = pid, sid
		}
		// 活动项目的工作区根跟随生效。
		if proj, ok := s.projects.ActiveProject(); ok && proj.WorkspaceRoot != "" {
			s.runtimeSettings.WorkspaceRoot = proj.WorkspaceRoot
			s.runtimeSettings.ExtraWritableRoots = proj.ExtraWritableRoots
		}
	}
	s.sessionID = sessionID
	s.projectID = projectID
	dir := filepath.Join(s.config.SessionsDir, projectID, sessionID)
	store, err := eventlog.Open(dir)
	if err != nil {
		s.eventStore = nil
		return
	}
	s.eventStore = store
}

func defaultProjectName(workspaceRoot string) string {
	name := baseNameFromPath(workspaceRoot)
	if name == "" {
		return "默认项目"
	}
	return name
}

// CheckpointInfo 是给前端 Timeline 的检查点摘要。
type CheckpointInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	TurnIndex int    `json:"turnIndex"`
	Timestamp string `json:"timestamp"`
	Preview   string `json:"preview"`
}

// recordUserEvent 记录一条用户消息事件。
func (s *Service) recordUserEvent(content string) {
	s.recordUserEventWithAttachments(content, nil)
}

func (s *Service) recordUserEventWithAttachments(content string, attachments []ChatAttachment) {
	if s.eventStore == nil {
		return
	}
	_, _ = s.appendEvent(eventlog.EventPayload{
		Role: "user", Content: redactSensitiveText(content), Attachments: toEventAttachments(attachments),
	}, eventlog.EventUserMessage)
}

func (s *Service) recordTurnTerminal(status, content, model string, parts []tools.ResultPart, durationMs int64) error {
	if s.eventStore == nil {
		return nil
	}
	if err := s.recordMessageArtifacts(parts); err != nil {
		return fmt.Errorf("记录任务终态产物失败: %w", err)
	}
	parts = s.mergeTurnTimelineParts(parts, content, status)
	_, err := s.appendEvent(eventlog.EventPayload{
		Role:         "assistant",
		Content:      redactSensitiveText(content),
		Model:        model,
		DurationMs:   durationMs,
		Parts:        toEventParts(parts),
		Status:       status,
		FailureState: toEventFailureStrategyState(s.failureStrategySnapshot()),
	}, eventlog.EventTurnTerminal)
	if err != nil {
		return fmt.Errorf("记录任务终止状态失败: %w", err)
	}
	if s.projects != nil {
		projectID, sessionID := s.projectID, s.sessionID
		if projectID != "" && sessionID != "" {
			_ = s.projects.TouchSession(projectID, sessionID)
		}
	}
	return nil
}

// recordFileSnapshot 在文件被修改后记录快照事件：把「改动前内容」写入内容寻址 blob。
func (s *Service) recordFileSnapshot(change tools.FileChange) error {
	if s.eventStore == nil {
		return nil
	}
	beforeHash := ""
	if change.Existed {
		h, err := s.eventStore.WriteSnapshot(change.Before)
		if err != nil {
			return fmt.Errorf("保存改动前快照失败: %w", err)
		}
		beforeHash = h
	}
	afterHash := ""
	if !change.Deleted {
		var err error
		afterHash, err = s.eventStore.WriteSnapshot(change.After)
		if err != nil {
			return fmt.Errorf("保存改动后快照失败: %w", err)
		}
	}
	_, err := s.appendEvent(eventlog.EventPayload{
		Path:            change.Path,
		BeforeHash:      beforeHash,
		AfterHash:       afterHash,
		LineEnding:      change.LineEnding,
		Encoding:        change.Encoding,
		HadBOM:          change.HadBOM,
		Existed:         change.Existed,
		Deleted:         change.Deleted,
		AfterLineEnding: change.AfterLineEnding,
		AfterEncoding:   change.AfterEncoding,
		AfterHadBOM:     change.AfterHadBOM,
	}, eventlog.EventFileSnapshot)
	if err != nil {
		return fmt.Errorf("记录文件改动事件失败: %w", err)
	}
	return nil
}

// recordAssistantAndCheckpoint 记录 assistant 消息与本轮 checkpoint。
func (s *Service) recordAssistantAndCheckpoint(content, model string, parts []tools.ResultPart, durations ...int64) {
	if s.eventStore == nil {
		return
	}
	parts = s.mergeTurnTimelineParts(parts, content, "completed")
	// Backfill provider- or test-produced file parts that did not pass through
	// executeToolCall. Normal tool results are already registered and skipped by
	// tool-call ID plus canonical path.
	_ = s.recordMessageArtifacts(parts)
	durationMs := int64(0)
	if len(durations) > 0 && durations[0] > 0 {
		durationMs = durations[0]
	}
	_, _ = s.appendEvent(eventlog.EventPayload{
		Role:         "assistant",
		Content:      content,
		Model:        model,
		DurationMs:   durationMs,
		Parts:        toEventParts(parts),
		FailureState: toEventFailureStrategyState(s.failureStrategySnapshot()),
	}, eventlog.EventAssistantMessage)
	_, _ = s.appendEvent(eventlog.EventPayload{
		Label:     truncateLabel(content),
		TurnIndex: s.sessionState.TurnCount,
	}, eventlog.EventCheckpoint)

	// 更新活动会话的时间戳与标题（首轮用用户/助手内容作标题）。
	if s.projects != nil {
		projectID, sessionID := s.projectID, s.sessionID
		if projectID == "" || sessionID == "" {
			projectID, sessionID = s.projects.ActiveIDs()
		}
		if s.sessionState.TurnCount <= 1 {
			if projectID != "" {
				_ = s.projects.SetSessionTitleForProject(projectID, sessionID, truncateLabel(content))
			} else {
				_ = s.projects.SetSessionTitle(sessionID, truncateLabel(content))
			}
		} else if projectID != "" {
			_ = s.projects.TouchSession(projectID, sessionID)
		} else {
			_ = s.projects.TouchActiveSession()
		}
	}
}

// ListCheckpoints 返回当前对话线的检查点（供前端 Timeline）。
func (s *Service) ListCheckpoints() []CheckpointInfo {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.eventStore == nil {
		return []CheckpointInfo{}
	}
	var out []CheckpointInfo
	for _, ev := range s.eventStore.Checkpoints() {
		out = append(out, CheckpointInfo{
			ID:        ev.ID,
			Label:     ev.Payload.Label,
			TurnIndex: ev.Payload.TurnIndex,
			Timestamp: ev.TS.Format(time.RFC3339),
			Preview:   ev.Payload.Label,
		})
	}
	if out == nil {
		return []CheckpointInfo{}
	}
	return out
}

// RewindToCheckpoint 回退到指定检查点：文件与对话一起回退。
//  1. 文件回退：把该点之后当前线上的所有 file_snapshot 逆序还原为改动前内容。
//  2. 对话回退：把 eventStore 的 head 移到该检查点，并从事件日志重建 sessionMessages。
//  3. 分叉：head 移动后，后续新对话将从该点自然分叉（旧线仍保留在磁盘）。
func (s *Service) RewindToCheckpoint(checkpointID string) (WorkbenchState, error) {
	release, err := s.beginActivity("rewinding the conversation")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.eventStore == nil {
		return s.workbenchStateLocked(), fmt.Errorf("未启用会话持久化，无法回退")
	}
	target, ok := s.eventStore.Event(checkpointID)
	if !ok || target.Type != eventlog.EventCheckpoint {
		return s.workbenchStateLocked(), fmt.Errorf("找不到检查点: %s", checkpointID)
	}
	if err := s.restoreCurrentBranchTo(checkpointID); err != nil {
		return s.workbenchStateLocked(), err
	}

	// 从事件日志重建 sessionMessages（保留 system 前缀）。
	s.rebuildSessionFromEvents()

	return s.workbenchStateLocked(), nil
}

// restoreCurrentBranchTo 将当前分支回退到它的某个祖先事件，并同步还原文件。
// targetID 为空时表示回到首条事件之前。
func (s *Service) restoreCurrentBranchTo(targetID string) error {
	if !s.eventStore.IsOnCurrentChain(targetID) {
		return fmt.Errorf("目标事件不在当前对话分支上: %s", targetID)
	}
	policy := s.sandboxPolicy()
	events := s.eventStore.EventsToUndo(targetID)
	if err := s.verifyCurrentFileStates(policy, events); err != nil {
		return err
	}
	backups, err := captureWorkspaceBackups(policy, eventPaths(events))
	if err != nil {
		return err
	}
	for _, ev := range events {
		if ev.Type != eventlog.EventFileSnapshot {
			continue
		}
		before := ""
		if ev.Payload.Existed {
			content, err := s.eventStore.ReadSnapshot(ev.Payload.BeforeHash)
			if err != nil {
				_ = restoreWorkspaceBackups(policy, backups)
				return fmt.Errorf("读取快照失败: %w", err)
			}
			before = content
		}
		if err := tools.RestoreFile(policy, ev.Payload.Path, before, ev.Payload.Existed, ev.Payload.LineEnding, ev.Payload.Encoding, ev.Payload.HadBOM); err != nil {
			_ = restoreWorkspaceBackups(policy, backups)
			return fmt.Errorf("恢复文件 %s 失败: %w", ev.Payload.Path, err)
		}
	}
	if err := s.eventStore.SetHead(targetID); err != nil {
		_ = restoreWorkspaceBackups(policy, backups)
		return err
	}
	return nil
}

type workspaceBackup struct {
	path       string
	content    string
	existed    bool
	lineEnding string
	encoding   string
	hadBOM     bool
}

func eventPaths(events []eventlog.Event) []string {
	seen := make(map[string]bool)
	paths := make([]string, 0)
	for _, ev := range events {
		if ev.Type != eventlog.EventFileSnapshot || seen[ev.Payload.Path] {
			continue
		}
		seen[ev.Payload.Path] = true
		paths = append(paths, ev.Payload.Path)
	}
	return paths
}

func captureWorkspaceBackups(policy tools.SandboxPolicy, paths []string) ([]workspaceBackup, error) {
	backups := make([]workspaceBackup, 0, len(paths))
	for _, path := range paths {
		abs, err := policy.ResolveWritePath(path)
		if err != nil {
			return nil, err
		}
		text, err := tools.ReadFileText(abs)
		if err == nil {
			backups = append(backups, workspaceBackup{path: path, content: text.Content, existed: true, lineEnding: string(text.LineEnding), encoding: string(text.Encoding), hadBOM: text.HadBOM})
			continue
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		backups = append(backups, workspaceBackup{path: path})
	}
	return backups, nil
}

func restoreWorkspaceBackups(policy tools.SandboxPolicy, backups []workspaceBackup) error {
	var firstErr error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		if err := tools.RestoreFile(policy, backup.path, backup.content, backup.existed, backup.lineEnding, backup.encoding, backup.hadBOM); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) verifyCurrentFileStates(policy tools.SandboxPolicy, events []eventlog.Event) error {
	seen := make(map[string]bool)
	for _, ev := range events {
		if ev.Type != eventlog.EventFileSnapshot || seen[ev.Payload.Path] {
			continue
		}
		seen[ev.Payload.Path] = true
		abs, err := policy.ResolveReadPath(ev.Payload.Path)
		if err != nil {
			return err
		}
		current, err := tools.ReadFileText(abs)
		if ev.Payload.Deleted {
			if os.IsNotExist(err) {
				continue
			}
			if err == nil {
				return fmt.Errorf("工作区文件 %s 已在 EchoCode 之外重新创建；为避免覆盖，已取消回退", ev.Payload.Path)
			}
			return err
		}
		if err != nil {
			return fmt.Errorf("工作区文件 %s 已在 EchoCode 之外被删除；为避免覆盖，已取消回退", ev.Payload.Path)
		}
		sum := sha256.Sum256([]byte(current.Content))
		if hex.EncodeToString(sum[:]) != ev.Payload.AfterHash {
			return fmt.Errorf("工作区文件 %s 在检查点之后被外部修改；请先保存或处理这些修改", ev.Payload.Path)
		}
	}
	return nil
}

// ForkFromMessage 从当前分支中的一条消息创建新分支。
// 用户消息从该提示词之前分叉，助手消息从其后的检查点继续。
func (s *Service) ForkFromMessage(messageEventID string) (WorkbenchState, error) {
	release, err := s.beginActivity("forking the conversation")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	return s.forkFromMessageLocked(messageEventID)
}

// ForkFromMessageForProjectSession refreshes the target log and forks it while
// holding one activity lock, so a detached chat runtime cannot leave a stale
// event head behind.
func (s *Service) ForkFromMessageForProjectSession(projectID, sessionID, messageEventID string) (WorkbenchState, error) {
	release, err := s.beginActivity("forking the conversation")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	activeProjectID, activeSessionID := s.activeSessionIDsLocked()
	if projectID != activeProjectID || sessionID != activeSessionID {
		return s.workbenchStateLocked(), fmt.Errorf("活动会话已切换，请在目标对话中重试")
	}
	s.activateCurrent()
	return s.forkFromMessageLocked(messageEventID)
}

func (s *Service) forkFromMessageLocked(messageEventID string) (WorkbenchState, error) {
	if s.eventStore == nil {
		return s.workbenchStateLocked(), fmt.Errorf("未启用会话持久化，无法分叉")
	}
	messageEventID = strings.TrimSpace(messageEventID)
	target, ok := s.eventStore.Event(messageEventID)
	if !ok || (target.Type != eventlog.EventUserMessage && target.Type != eventlog.EventAssistantMessage) {
		return s.workbenchStateLocked(), fmt.Errorf("找不到可分叉的消息: %s", messageEventID)
	}
	if !s.eventStore.IsOnCurrentChain(messageEventID) {
		return s.workbenchStateLocked(), fmt.Errorf("消息不在当前对话分支上")
	}

	branchHead := target.ParentID
	label := truncateLabel("从“" + target.Payload.Content + "”分叉")
	if target.Type == eventlog.EventAssistantMessage {
		branchHead = target.ID
		chain := s.eventStore.Events()
		for index, ev := range chain {
			if ev.ID != target.ID {
				continue
			}
			for _, next := range chain[index+1:] {
				if next.Type == eventlog.EventCheckpoint {
					branchHead = next.ID
					break
				}
				if next.Type == eventlog.EventUserMessage {
					break
				}
			}
			break
		}
	}

	originalHead := s.eventStore.Head()
	if err := s.restoreCurrentBranchTo(branchHead); err != nil {
		return s.workbenchStateLocked(), err
	}
	if _, err := s.appendEvent(eventlog.EventPayload{Label: label}, eventlog.EventBranchMarker); err != nil {
		// 尽力恢复原分支，避免写入失败后文件与对话停在半完成状态。
		_, _ = s.switchBranch(originalHead)
		return s.workbenchStateLocked(), fmt.Errorf("创建对话分支失败: %w", err)
	}
	s.rebuildSessionFromEvents()
	if s.projects != nil {
		_ = s.projects.TouchActiveSession()
	}
	return s.workbenchStateLocked(), nil
}

// rebuildSessionFromEvents 依据当前对话线的事件重建内存中的 sessionMessages 与计数。
// 保留原有的 system 稳定前缀（第 0 条），仅重排其后的 user/assistant。
func (s *Service) rebuildSessionFromEvents() {
	if s.eventStore == nil {
		return
	}
	events := s.eventStore.Events()
	artifactRecords := artifactRecordsFromEvents(events, s.projectID, s.sessionID)
	hasArtifactRegistry := len(artifactRecords) > 0
	var systemMsg *protocol.Message
	if len(s.sessionMessages) > 0 && s.sessionMessages[0].Role == "system" {
		m := s.sessionMessages[0]
		systemMsg = &m
	}

	rebuilt := make([]protocol.Message, 0, 8)
	if systemMsg != nil {
		rebuilt = append(rebuilt, *systemMsg)
	}
	appendAssistant := func(content string, parts []tools.ResultPart, terminalStatus string) {
		assistantIndex := len(rebuilt)
		if !hasArtifactRegistry {
			rebuilt = s.appendProtocolAssistantMessage(rebuilt, content, parts)
		} else {
			message := s.protocolAssistantMessage(stripPrivateAssistantContext(content), parts)
			message.Content = stripLocalArtifactContext(message.Content)
			rebuilt = append(rebuilt, message)
			if context := formatFailureStrategyContext(s.failureStrategySnapshot()); context != "" {
				rebuilt = append(rebuilt, protocol.Message{Role: "system", Content: context, InternalKind: contextFailureStrategyKind})
			}
		}
		if strings.TrimSpace(terminalStatus) != "" && assistantIndex < len(rebuilt) && rebuilt[assistantIndex].Role == "assistant" {
			rebuilt[assistantIndex].InternalKind = terminalTurnInternalKind(terminalStatus)
		}
	}
	turns := 0
	pendingUser := false
	s.planState = PlanState{}
	s.replaceFailureStrategyState(failureStrategyState{})
	s.teamResume = nil
	s.teamState = TeamState{Enabled: s.runtimeSettings.Team.Enabled, Status: "idle", Roles: []TeamRoleState{}}
	teamFailureAwaitingTerminal := false
	compressionCount := 0
	compressedMessageCount := 0
	lastCompressedAt := ""
	for _, ev := range events {
		switch ev.Type {
		case eventlog.EventUserMessage:
			teamFailureAwaitingTerminal = false
			attachments := fromEventAttachments(ev.Payload.Attachments)
			if context := markdownReferenceRequestContext(attachments); context != "" {
				rebuilt = append(rebuilt, protocol.Message{
					Role: "user", Content: context, InternalKind: contextRequestKind,
				})
			}
			rebuilt = append(rebuilt, protocol.Message{
				Role: "user", Content: ev.Payload.Content, Attachments: protocolAttachments(attachments),
			})
			pendingUser = true
		case eventlog.EventAssistantMessage:
			teamFailureAwaitingTerminal = false
			if failureState, ok := fromEventFailureStrategyState(ev.Payload.FailureState); ok {
				s.replaceFailureStrategyState(failureState)
			}
			content, parts := restoredAssistantMessage(ev.Payload.Content, fromEventParts(ev.Payload.Parts))
			appendAssistant(content, parts, "")
			pendingUser = false
		case eventlog.EventTurnTerminal:
			if failureState, ok := fromEventFailureStrategyState(ev.Payload.FailureState); ok {
				s.replaceFailureStrategyState(failureState)
			}
			// A retained interrupted turn has its user event on the branch. A
			// zero-output failure is rolled back before the terminal marker, so it
			// must not become an orphan assistant message in future model context.
			if pendingUser || (teamFailureAwaitingTerminal && ev.Payload.Status == "failed") {
				appendAssistant(ev.Payload.Content, fromEventParts(ev.Payload.Parts), ev.Payload.Status)
				pendingUser = false
				turns++
			}
			teamFailureAwaitingTerminal = false
		case eventlog.EventCheckpoint:
			teamFailureAwaitingTerminal = false
			turns++
		case eventlog.EventPlanUpdate:
			steps := make([]tools.ProgressStep, 0, len(ev.Payload.PlanSteps))
			for _, step := range ev.Payload.PlanSteps {
				steps = append(steps, tools.ProgressStep{Title: step.Title, Status: step.Status})
			}
			s.planState = PlanState{
				Revision:  s.planState.Revision + 1,
				Status:    ev.Payload.PlanStatus,
				Steps:     steps,
				UpdatedAt: ev.TS.Format(time.RFC3339),
			}
		case eventlog.EventTeamCheckpoint:
			checkpoint := s.restoreTeamRunCheckpoint(ev.Payload.TeamCheckpointHash)
			if checkpoint == nil {
				continue
			}
			if checkpoint.Status == "running" {
				checkpoint.Status = "paused"
				checkpoint.Team.Active = false
				checkpoint.Team.Status = "paused"
				for index := range checkpoint.Team.Roles {
					if checkpoint.Team.Roles[index].Status == "running" {
						checkpoint.Team.Roles[index].Status = "paused"
					}
				}
			}
			s.teamState = cloneTeamState(checkpoint.Team)
			teamFailureAwaitingTerminal = checkpoint.Status == "failed"
			if checkpoint.Status == "paused" {
				s.teamResume = checkpoint
			} else {
				s.teamResume = nil
			}
		case eventlog.EventContextCondensed:
			if strings.TrimSpace(ev.Payload.ContextViewHash) == "" {
				continue
			}
			encoded, err := s.eventStore.ReadSnapshot(ev.Payload.ContextViewHash)
			if err != nil {
				continue
			}
			snapshot, err := decodePersistedContextView([]byte(encoded))
			if err != nil {
				continue
			}
			prefix := make([]protocol.Message, 0, len(snapshot)+1)
			if systemMsg != nil {
				prefix = append(prefix, *systemMsg)
			}
			rebuilt = append(prefix, cloneProtocolMessages(snapshot)...)
			pendingUser = false
			compressionCount++
			compressedMessageCount += ev.Payload.ContextRemovedMessages
			lastCompressedAt = ev.TS.Format(time.RFC3339)
		}
	}
	if hasArtifactRegistry {
		context := formatLocalArtifactContext(artifactReferencesFromRecords(artifactRecords), 0)
		if context != "" {
			index := 0
			if len(rebuilt) > 0 && rebuilt[0].Role == "system" && rebuilt[0].InternalKind == "" {
				index = 1
			}
			rebuilt = append(rebuilt, protocol.Message{})
			copy(rebuilt[index+1:], rebuilt[index:len(rebuilt)-1])
			rebuilt[index] = protocol.Message{Role: "system", Content: context, InternalKind: contextArtifactKind}
		}
	}
	s.sessionMessages = rebuilt
	s.sessionState.MessageCount = len(rebuilt)
	s.sessionState.TurnCount = turns
	s.sessionState.ContextWindowTokens = 0
	s.sessionState.ContextWindowSource = ""
	s.sessionState.EstimatedInputTokens = 0
	s.sessionState.InputBudgetTokens = 0
	s.sessionState.ContextUsagePercent = 0
	s.sessionState.CompressionCount = compressionCount
	s.sessionState.CompressedMessageCount = compressedMessageCount
	s.sessionState.LastCompressedAt = lastCompressedAt
	s.lastRequest = nil
}

// SessionMessage 是给前端的一条历史消息（含结构化片段）。
type SessionMessage struct {
	ID          string             `json:"id"`
	Role        string             `json:"role"`
	Content     string             `json:"content"`
	Model       string             `json:"model,omitempty"`
	CreatedAt   string             `json:"createdAt"`
	DurationMs  int64              `json:"durationMs,omitempty"`
	Parts       []tools.ResultPart `json:"parts,omitempty"`
	Attachments []ChatAttachment   `json:"attachments,omitempty"`
	Status      string             `json:"status,omitempty"`
}

// GetSessionMessages 返回当前活动会话的历史消息（从事件日志重建），
// 供前端启动/切换会话时恢复对话内容。修复"关闭打开对话消失"。
func (s *Service) GetSessionMessages() []SessionMessage {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.eventStore == nil {
		return []SessionMessage{}
	}
	return sessionMessagesFromEventStore(s.eventStore)
}

// GetSessionMessagesForSession opens a fresh read view for a detached session.
// The active Service may have opened its event log before a background task
// appended new events, so this method intentionally does not reuse eventStore.
func (s *Service) GetSessionMessagesForSession(sessionID string) []SessionMessage {
	messages, err := s.GetSessionMessagesForProjectSession("", sessionID)
	if err != nil {
		return []SessionMessage{}
	}
	return messages
}

// GetSessionMessagesForProjectSession distinguishes a valid empty conversation
// from a missing/corrupt session so callers never erase visible history on a
// lookup failure.
func (s *Service) GetSessionMessagesForProjectSession(projectID, sessionID string) ([]SessionMessage, error) {
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	s.stateMu.RLock()
	projects := s.projects
	sessionsDir := s.config.SessionsDir
	s.stateMu.RUnlock()
	if projects == nil || strings.TrimSpace(sessionsDir) == "" {
		return nil, fmt.Errorf("会话历史存储不可用")
	}
	if projectID == "" {
		var ok bool
		projectID, _, ok = projects.FindSession(sessionID)
		if !ok {
			return nil, fmt.Errorf("会话不存在或 ID 不唯一: %s", sessionID)
		}
	} else if _, ok := projects.ProjectSession(projectID, sessionID); !ok {
		return nil, fmt.Errorf("项目 %s 中不存在会话: %s", projectID, sessionID)
	}
	store, err := eventlog.Open(filepath.Join(sessionsDir, projectID, sessionID))
	if err != nil {
		return nil, fmt.Errorf("读取会话历史失败: %w", err)
	}
	return sessionMessagesFromEventStore(store), nil
}

func sessionMessagesFromEventStore(store *eventlog.Store) []SessionMessage {
	if store == nil {
		return []SessionMessage{}
	}
	out := make([]SessionMessage, 0, 16)
	for _, ev := range store.Events() {
		switch ev.Type {
		case eventlog.EventUserMessage:
			out = append(out, SessionMessage{
				ID:          ev.ID,
				Role:        "user",
				Content:     ev.Payload.Content,
				CreatedAt:   ev.TS.Format(time.RFC3339),
				Attachments: fromEventAttachments(ev.Payload.Attachments),
			})
		case eventlog.EventAssistantMessage:
			parts := fromEventParts(ev.Payload.Parts)
			content, parts := restoredAssistantMessage(ev.Payload.Content, parts)
			out = append(out, SessionMessage{
				ID:         ev.ID,
				Role:       "assistant",
				Content:    content,
				Model:      ev.Payload.Model,
				CreatedAt:  ev.TS.Format(time.RFC3339),
				DurationMs: ev.Payload.DurationMs,
				Parts:      parts,
			})
		case eventlog.EventTurnTerminal:
			out = append(out, SessionMessage{
				ID:         ev.ID,
				Role:       "assistant",
				Content:    ev.Payload.Content,
				Model:      ev.Payload.Model,
				CreatedAt:  ev.TS.Format(time.RFC3339),
				DurationMs: ev.Payload.DurationMs,
				Parts:      fromEventParts(ev.Payload.Parts),
				Status:     ev.Payload.Status,
			})
		}
	}
	return out
}

func toEventAttachments(attachments []ChatAttachment) []eventlog.MessageAttachment {
	if len(attachments) == 0 {
		return nil
	}
	converted := make([]eventlog.MessageAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		converted = append(converted, eventlog.MessageAttachment{
			Kind: attachment.Kind, Name: attachment.Name, MIMEType: attachment.MIMEType, Data: attachment.Data,
			Size: attachment.Size, CharacterCount: attachment.CharacterCount,
		})
	}
	return converted
}

func fromEventAttachments(attachments []eventlog.MessageAttachment) []ChatAttachment {
	if len(attachments) == 0 {
		return nil
	}
	converted := make([]ChatAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		converted = append(converted, ChatAttachment{
			Kind: attachment.Kind, Name: attachment.Name, MIMEType: attachment.MIMEType, Data: attachment.Data,
			Size: attachment.Size, CharacterCount: attachment.CharacterCount,
		})
	}
	return converted
}

func restoredAssistantMessage(content string, parts []tools.ResultPart) (string, []tools.ResultPart) {
	if !isLegacyWebSearchPlaceholder(content) {
		return content, parts
	}

	var searchPart tools.ResultPart
	foundSources := false
	for _, part := range parts {
		if part.Kind == tools.PartWebSearch && len(part.Sources) > 0 {
			searchPart = part
			foundSources = true
			break
		}
	}
	if !foundSources {
		return content, parts
	}

	fallback := webSearchFallbackContent(searchPart)
	restoredParts := append([]tools.ResultPart(nil), parts...)
	replacedText := false
	for index := range restoredParts {
		if restoredParts[index].Kind == tools.PartText && isLegacyWebSearchPlaceholder(restoredParts[index].Text) {
			restoredParts[index].Text = fallback
			replacedText = true
		}
	}
	if !replacedText {
		restoredParts = append(restoredParts, tools.ResultPart{Kind: tools.PartText, Text: fallback})
	}
	return fallback, restoredParts
}

func isLegacyWebSearchPlaceholder(content string) bool {
	switch strings.TrimSpace(content) {
	case "网络搜索已完成，但上游模型在整理结果时连接失败。搜索来源已经保留，请展开搜索记录查看；也可以直接重试本条消息。",
		"网络搜索已完成，但上游模型没有返回整理后的正文。搜索来源已经保留，请展开搜索记录查看；也可以直接重试本条消息。":
		return true
	default:
		return false
	}
}

// fromEventParts 把事件日志的片段还原为 tools.ResultPart（对齐前端 MessagePart）。
func fromEventParts(parts []eventlog.MessagePart) []tools.ResultPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]tools.ResultPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, tools.ResultPart{
			Kind:              tools.PartKind(p.Kind),
			Text:              p.Text,
			Path:              p.Path,
			Patch:             p.Patch,
			Additions:         p.Additions,
			Deletions:         p.Deletions,
			LineCount:         p.LineCount,
			Created:           p.Created,
			FileAction:        p.FileAction,
			Name:              p.Name,
			Status:            p.Status,
			Input:             p.Input,
			Output:            p.Output,
			ToolCallID:        p.ToolCallID,
			Stdout:            p.Stdout,
			Stderr:            p.Stderr,
			WorkingDirectory:  p.WorkingDirectory,
			ExitCode:          p.ExitCode,
			StartedAt:         p.StartedAt,
			CompletedAt:       p.CompletedAt,
			DurationMs:        p.DurationMs,
			Steps:             fromEventProgressSteps(p.Steps),
			TaskStatus:        p.TaskStatus,
			ChangedFiles:      p.ChangedFiles,
			Query:             p.Query,
			Sources:           fromEventSearchSources(p.Sources),
			Role:              p.Role,
			RoleLabel:         p.RoleLabel,
			ProviderID:        p.ProviderID,
			Model:             p.Model,
			Summary:           p.Summary,
			Verdict:           p.Verdict,
			Attempt:           p.Attempt,
			TaskID:            p.TaskID,
			AgentType:         p.AgentType,
			Label:             p.Label,
			CurrentAction:     p.CurrentAction,
			SubagentOutput:    p.SubagentOutput,
			SubagentReasoning: p.SubagentReasoning,
			Activities:        fromEventSubagentActivities(p.Activities),
			NoticeKind:        p.NoticeKind,
			Severity:          p.Severity,
			Message:           p.Message,
			RequestedModel:    p.RequestedModel,
			EffectiveModel:    p.EffectiveModel,
			RetryModel:        p.RetryModel,
			UseCases:          append([]string(nil), p.UseCases...),
			Reasons:           append([]string(nil), p.Reasons...),
			Verifications:     append([]string(nil), p.Verifications...),
			MetadataKeys:      append([]string(nil), p.MetadataKeys...),
			RequestID:         p.RequestID,
			ErrorCode:         p.ErrorCode,
			HTTPStatus:        p.HTTPStatus,
			Retryable:         p.Retryable,
			SecretID:          p.SecretID,
			SecretLabel:       p.SecretLabel,
			SecretSource:      p.SecretSource,
		})
	}
	return out
}

// sandboxPolicy 从 runtime settings 构造 tools.SandboxPolicy（供 rewind 恢复文件用）。
func (s *Service) sandboxPolicy() tools.SandboxPolicy {
	return tools.SandboxPolicy{
		SandboxMode:          s.runtimeSettings.SandboxMode,
		WorkspaceRoot:        s.runtimeSettings.WorkspaceRoot,
		ExtraWritableRoots:   s.runtimeSettings.ExtraWritableRoots,
		FilesystemAccess:     s.runtimeSettings.FilesystemAccess,
		NetworkAccess:        s.runtimeSettings.NetworkAccess,
		ShellAccess:          s.runtimeSettings.ShellAccess,
		AllowDestructiveOps:  s.runtimeSettings.AllowDestructiveOps,
		MaxCommandSeconds:    s.runtimeSettings.MaxCommandSeconds,
		MaxCommandMemoryMB:   s.runtimeSettings.MaxCommandMemoryMB,
		MaxCommandCPUPercent: s.runtimeSettings.MaxCommandCPUPercent,
		MaxCommandProcesses:  s.runtimeSettings.MaxCommandProcesses,
	}
}

// BranchInfo 是给前端的分支（对话线）摘要。
type BranchInfo struct {
	LeafID    string `json:"leafId"`    // 该分支的叶子事件 ID
	Label     string `json:"label"`     // 分支标签（最后一个 checkpoint 摘要）
	TurnCount int    `json:"turnCount"` // 该分支的轮数
	Timestamp string `json:"timestamp"` // 叶子事件时间
	IsCurrent bool   `json:"isCurrent"` // 是否为当前分支
}

// ListBranches 返回所有对话线（分支）。当前分支标记 IsCurrent。
func (s *Service) ListBranches() []BranchInfo {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.eventStore == nil {
		return []BranchInfo{}
	}
	head := s.eventStore.Head()
	currentLeaf := s.currentLeaf(head)

	var out []BranchInfo
	for _, leaf := range s.eventStore.Leaves() {
		turns := 0
		for _, ev := range s.eventStore.Chain(leaf.ID) {
			if ev.Type == eventlog.EventCheckpoint {
				turns++
			}
		}
		label := "（空分支）"
		chain := s.eventStore.Chain(leaf.ID)
		for index := len(chain) - 1; index >= 0; index-- {
			ev := chain[index]
			if (ev.Type == eventlog.EventBranchMarker || ev.Type == eventlog.EventCheckpoint) && strings.TrimSpace(ev.Payload.Label) != "" {
				label = ev.Payload.Label
				break
			}
		}
		out = append(out, BranchInfo{
			LeafID:    leaf.ID,
			Label:     label,
			TurnCount: turns,
			Timestamp: leaf.TS.Format(time.RFC3339),
			IsCurrent: leaf.ID == currentLeaf,
		})
	}
	if out == nil {
		return []BranchInfo{}
	}
	return out
}

// currentLeaf 返回包含 head 的那条线的叶子 ID（head 本身即在其线的末端时就是它）。
func (s *Service) currentLeaf(head string) string {
	// head 所在线的叶子：在所有叶子里找 chain 包含 head 的那个。
	for _, leaf := range s.eventStore.Leaves() {
		for _, ev := range s.eventStore.Chain(leaf.ID) {
			if ev.ID == head {
				return leaf.ID
			}
		}
	}
	return head
}

// SwitchBranch 切换到另一条对话线（叶子）：把文件与对话都切到目标分支的末端状态。
//  1. 求当前 head 与目标叶子的公共祖先。
//  2. 文件回退：撤销当前线从祖先到 head 的改动（逆序还原 before-image）。
//  3. 文件重放：应用目标线从祖先到叶子的改动（顺序写入 after-image）。
//  4. head 移到目标叶子，重建对话。
func (s *Service) SwitchBranch(leafID string) (WorkbenchState, error) {
	release, err := s.beginActivity("switching conversation branches")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	return s.switchBranch(leafID)
}

func (s *Service) switchBranch(leafID string) (WorkbenchState, error) {
	if s.eventStore == nil {
		return s.workbenchStateLocked(), fmt.Errorf("未启用会话持久化，无法切换分支")
	}
	head := s.eventStore.Head()
	if head == leafID {
		return s.workbenchStateLocked(), nil
	}
	ancestor := s.eventStore.CommonAncestor(head, leafID)
	policy := s.sandboxPolicy()
	undoEvents := s.eventStore.EventsToUndo(ancestor)
	replayEvents := s.eventStore.FileSnapshotsBetween(ancestor, leafID)
	if err := s.verifyCurrentFileStates(policy, undoEvents); err != nil {
		return s.workbenchStateLocked(), err
	}
	allEvents := append(append([]eventlog.Event{}, undoEvents...), replayEvents...)
	backups, err := captureWorkspaceBackups(policy, eventPaths(allEvents))
	if err != nil {
		return s.workbenchStateLocked(), err
	}
	restoreOnError := func(cause error) (WorkbenchState, error) {
		if restoreErr := restoreWorkspaceBackups(policy, backups); restoreErr != nil {
			return s.workbenchStateLocked(), fmt.Errorf("%v; restoring workspace also failed: %w", cause, restoreErr)
		}
		return s.workbenchStateLocked(), cause
	}

	// 2. 撤销当前线：从 head 回到祖先，逆序还原 before-image。
	for _, ev := range undoEvents {
		if ev.Type != eventlog.EventFileSnapshot {
			continue
		}
		before := ""
		if ev.Payload.Existed {
			content, err := s.eventStore.ReadSnapshot(ev.Payload.BeforeHash)
			if err != nil {
				return restoreOnError(fmt.Errorf("读取快照失败: %w", err))
			}
			before = content
		}
		if err := tools.RestoreFile(policy, ev.Payload.Path, before, ev.Payload.Existed, ev.Payload.LineEnding, ev.Payload.Encoding, ev.Payload.HadBOM); err != nil {
			return restoreOnError(fmt.Errorf("撤销文件 %s 失败: %w", ev.Payload.Path, err))
		}
	}

	// 3. 重放目标线：从祖先到叶子，顺序写入 after-image。
	for _, ev := range replayEvents {
		after := ""
		if !ev.Payload.Deleted {
			var err error
			after, err = s.eventStore.ReadSnapshot(ev.Payload.AfterHash)
			if err != nil {
				return restoreOnError(fmt.Errorf("读取快照失败: %w", err))
			}
		}
		afterLineEnding := ev.Payload.AfterLineEnding
		if afterLineEnding == "" {
			afterLineEnding = ev.Payload.LineEnding
		}
		afterEncoding := ev.Payload.AfterEncoding
		if afterEncoding == "" {
			afterEncoding = ev.Payload.Encoding
		}
		afterHadBOM := ev.Payload.AfterHadBOM
		if ev.Payload.AfterEncoding == "" && ev.Payload.AfterLineEnding == "" {
			afterHadBOM = ev.Payload.HadBOM
		}
		if err := tools.RestoreFile(policy, ev.Payload.Path, after, !ev.Payload.Deleted, afterLineEnding, afterEncoding, afterHadBOM); err != nil {
			return restoreOnError(fmt.Errorf("应用文件 %s 失败: %w", ev.Payload.Path, err))
		}
	}

	// 4. 移动 head 到目标叶子并重建对话。
	if err := s.eventStore.SetHead(leafID); err != nil {
		return restoreOnError(err)
	}
	s.rebuildSessionFromEvents()
	return s.workbenchStateLocked(), nil
}

func toEventParts(parts []tools.ResultPart) []eventlog.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]eventlog.MessagePart, 0, len(parts))
	for _, p := range parts {
		out = append(out, eventlog.MessagePart{
			Kind:              string(p.Kind),
			Text:              p.Text,
			Path:              p.Path,
			Patch:             p.Patch,
			Additions:         p.Additions,
			Deletions:         p.Deletions,
			LineCount:         p.LineCount,
			Created:           p.Created,
			FileAction:        p.FileAction,
			Name:              p.Name,
			Status:            p.Status,
			Input:             p.Input,
			Output:            p.Output,
			ToolCallID:        p.ToolCallID,
			Stdout:            p.Stdout,
			Stderr:            p.Stderr,
			WorkingDirectory:  p.WorkingDirectory,
			ExitCode:          p.ExitCode,
			StartedAt:         p.StartedAt,
			CompletedAt:       p.CompletedAt,
			DurationMs:        p.DurationMs,
			Steps:             toEventProgressSteps(p.Steps),
			TaskStatus:        p.TaskStatus,
			ChangedFiles:      p.ChangedFiles,
			Query:             p.Query,
			Sources:           toEventSearchSources(p.Sources),
			Role:              p.Role,
			RoleLabel:         p.RoleLabel,
			ProviderID:        p.ProviderID,
			Model:             p.Model,
			Summary:           p.Summary,
			Verdict:           p.Verdict,
			Attempt:           p.Attempt,
			TaskID:            p.TaskID,
			AgentType:         p.AgentType,
			Label:             p.Label,
			CurrentAction:     p.CurrentAction,
			SubagentOutput:    p.SubagentOutput,
			SubagentReasoning: p.SubagentReasoning,
			Activities:        toEventSubagentActivities(p.Activities),
			NoticeKind:        p.NoticeKind,
			Severity:          p.Severity,
			Message:           p.Message,
			RequestedModel:    p.RequestedModel,
			EffectiveModel:    p.EffectiveModel,
			RetryModel:        p.RetryModel,
			UseCases:          append([]string(nil), p.UseCases...),
			Reasons:           append([]string(nil), p.Reasons...),
			Verifications:     append([]string(nil), p.Verifications...),
			MetadataKeys:      append([]string(nil), p.MetadataKeys...),
			RequestID:         p.RequestID,
			ErrorCode:         p.ErrorCode,
			HTTPStatus:        p.HTTPStatus,
			Retryable:         p.Retryable,
			SecretID:          p.SecretID,
			SecretLabel:       p.SecretLabel,
			SecretSource:      p.SecretSource,
		})
	}
	return out
}

func toEventProgressSteps(steps []tools.ProgressStep) []eventlog.MessageProgressStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]eventlog.MessageProgressStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, eventlog.MessageProgressStep{Title: step.Title, Status: step.Status})
	}
	return out
}

func fromEventProgressSteps(steps []eventlog.MessageProgressStep) []tools.ProgressStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]tools.ProgressStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, tools.ProgressStep{Title: step.Title, Status: step.Status})
	}
	return out
}

func toEventSearchSources(sources []tools.SearchSource) []eventlog.MessageSearchSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]eventlog.MessageSearchSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, eventlog.MessageSearchSource{Title: source.Title, URL: source.URL, Snippet: source.Snippet})
	}
	return out
}

func fromEventSearchSources(sources []eventlog.MessageSearchSource) []tools.SearchSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]tools.SearchSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, tools.SearchSource{Title: source.Title, URL: source.URL, Snippet: source.Snippet})
	}
	return out
}

func toEventSubagentActivities(activities []tools.SubagentActivity) []eventlog.SubagentActivity {
	if len(activities) == 0 {
		return nil
	}
	out := make([]eventlog.SubagentActivity, 0, len(activities))
	for _, activity := range activities {
		out = append(out, eventlog.SubagentActivity{
			ID: activity.ID, Kind: activity.Kind, Title: activity.Title, Status: activity.Status,
			Input: activity.Input, Output: activity.Output, StartedAt: activity.StartedAt,
			CompletedAt: activity.CompletedAt, DurationMs: activity.DurationMs,
		})
	}
	return out
}

func fromEventSubagentActivities(activities []eventlog.SubagentActivity) []tools.SubagentActivity {
	if len(activities) == 0 {
		return nil
	}
	out := make([]tools.SubagentActivity, 0, len(activities))
	for _, activity := range activities {
		out = append(out, tools.SubagentActivity{
			ID: activity.ID, Kind: activity.Kind, Title: activity.Title, Status: activity.Status,
			Input: activity.Input, Output: activity.Output, StartedAt: activity.StartedAt,
			CompletedAt: activity.CompletedAt, DurationMs: activity.DurationMs,
		})
	}
	return out
}

func truncateLabel(s string) string {
	const limit = 40
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
