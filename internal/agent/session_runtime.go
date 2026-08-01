package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/project"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

// ActiveSessionIDs returns the manifest pointers without changing them.
// Chat task runners use it only to bind a new task to the current conversation.
func (s *Service) ActiveSessionIDs() (projectID, sessionID string) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.activeSessionIDsLocked()
}

func (s *Service) activeSessionIDsLocked() (projectID, sessionID string) {
	if s.projectID != "" && s.sessionID != "" {
		return s.projectID, s.sessionID
	}
	if s.projects == nil {
		return s.projectID, s.sessionID
	}
	return s.projects.ActiveIDs()
}

// NewSessionRuntime creates an isolated Agent state machine for one session.
// Provider configuration, MCP, credentials, and host bridges are shared, while
// conversation history, event log, compression counters, plan/team state,
// approvals, and usage samples are private to the returned runtime.
//
// The application-level active session is deliberately not changed. This is
// what allows a user to switch conversations while another task is streaming.
func (s *Service) NewSessionRuntime(sessionID string) (*Service, error) {
	return s.NewProjectSessionRuntime("", sessionID)
}

// NewProjectSessionRuntime creates a runtime for an exact project/session pair.
// A blank project ID is retained only for legacy callers and must resolve to a
// globally unambiguous session ID.
func (s *Service) NewProjectSessionRuntime(projectID, sessionID string) (*Service, error) {
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		projectID, sessionID = s.ActiveSessionIDs()
	}

	s.stateMu.RLock()
	projects := s.projects
	config := s.config
	settings := cloneRuntimeSettings(s.runtimeSettings)
	reasoning := s.reasoning
	deepSeekState := cloneDeepSeekState(s.deepSeekState)
	secretVault := s.secretVault
	builder := s.builder
	mcpManager := s.mcpManager
	pluginManager := s.pluginManager
	usageStore := s.usageStore
	usageLedger := s.usageLedger
	planMode := s.planMode
	providerFactory := s.providerFactory
	providerStatusSink := s.providerStatusSink
	if providerStatusSink == nil {
		providerStatusSink = s.applySessionProviderStatus
	}
	installationID := s.installationID
	anthropicCompatibilityCache := s.anthropicCompatibilityCache
	anthropicCompatibilitySink := s.anthropicCompatibilitySink
	if anthropicCompatibilitySink == nil {
		anthropicCompatibilitySink = s.applySessionAnthropicCompatibility
	}
	baseMessages := cloneProtocolMessages(s.sessionMessages)
	baseSessionState := s.sessionState
	teamState := cloneTeamState(s.teamState)
	teamResume := cloneTeamRunCheckpoint(s.teamResume)
	failureStrategy := s.failureStrategySnapshot()
	toolMutationGates := s.toolMutationGates
	resourceCoordinators := s.resourceCoordinators
	snapshot := cloneWorkbenchState(s.stateSnapshot)
	var notify func(ApprovalRequest)
	if s.approvals != nil {
		notify = s.approvals.notification()
	}
	s.stateMu.RUnlock()
	if installationID == "" {
		installationID = stableInstallationID(config)
	}
	if anthropicCompatibilityCache == nil {
		anthropicCompatibilityCache = protocol.NewAnthropicCompatibilityCache()
	}

	if projects == nil || strings.TrimSpace(config.SessionsDir) == "" {
		// In-memory/test services have no persistent session directory. They can
		// still get an isolated copy, but there is no event log to reopen.
		runtime := &Service{
			config:                      config,
			reasoning:                   reasoning,
			deepSeekState:               deepSeekState,
			runtimeSettings:             settings,
			settingsPath:                config.SettingsPath,
			secretVault:                 secretVault,
			builder:                     builder,
			mcpManager:                  mcpManager,
			pluginManager:               pluginManager,
			usageStore:                  usageStore,
			usageLedger:                 usageLedger,
			planMode:                    planMode,
			providerFactory:             providerFactory,
			providerStatusSink:          providerStatusSink,
			installationID:              installationID,
			anthropicCompatibilityCache: anthropicCompatibilityCache,
			anthropicCompatibilitySink:  anthropicCompatibilitySink,
			detachedSessionRuntime:      true,
			approvals:                   newApprovalBroker(),
			sessionID:                   sessionID,
			projectID:                   projectID,
			sessionMessages:             baseMessages,
			sessionState:                baseSessionState,
			failureStrategy:             failureStrategy,
			toolMutationGates:           toolMutationGates,
			resourceCoordinators:        resourceCoordinators,
		}
		runtime.approvals.SetNotify(notify)
		runtime.teamState = teamState
		runtime.teamResume = teamResume
		runtime.stateSnapshot = snapshot
		return runtime, nil
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
	workspace, ok := projects.Project(projectID)
	if !ok {
		return nil, fmt.Errorf("项目不存在: %s", projectID)
	}
	if strings.TrimSpace(workspace.WorkspaceRoot) != "" {
		settings.WorkspaceRoot = workspace.WorkspaceRoot
		settings.ExtraWritableRoots = append([]string(nil), workspace.ExtraWritableRoots...)
	}

	runtime := &Service{
		config:                      config,
		reasoning:                   reasoning,
		deepSeekState:               deepSeekState,
		runtimeSettings:             settings,
		settingsPath:                config.SettingsPath,
		secretVault:                 secretVault,
		builder:                     builder,
		mcpManager:                  mcpManager,
		pluginManager:               pluginManager,
		usageStore:                  usageStore,
		usageLedger:                 usageLedger,
		planMode:                    planMode,
		providerFactory:             providerFactory,
		providerStatusSink:          providerStatusSink,
		installationID:              installationID,
		anthropicCompatibilityCache: anthropicCompatibilityCache,
		anthropicCompatibilitySink:  anthropicCompatibilitySink,
		detachedSessionRuntime:      true,
		projects:                    projects,
		approvals:                   newApprovalBroker(),
		sessionID:                   sessionID,
		projectID:                   projectID,
		teamState:                   TeamState{Enabled: settings.Team.Enabled, Status: "idle", Roles: []TeamRoleState{}},
		toolMutationGates:           toolMutationGates,
		resourceCoordinators:        resourceCoordinators,
	}
	runtime.approvals.SetNotify(notify)

	store, err := eventlog.Open(filepath.Join(config.SessionsDir, projectID, sessionID))
	if err != nil {
		return nil, fmt.Errorf("打开会话事件日志失败: %w", err)
	}
	runtime.eventStore = store
	runtime.rebuildSessionFromEvents()
	runtime.restoreUsageMetrics()
	runtime.refreshProjectMemory()
	runtime.storeWorkbenchSnapshot(runtime.workbenchStateLocked())
	return runtime, nil
}

// ReloadProjectSessionIfActive adopts events written by a detached task
// runtime without changing the user's current project/session selection.
func (s *Service) ReloadProjectSessionIfActive(projectID, sessionID string) (WorkbenchState, bool, error) {
	release, err := s.beginActivity("reloading a completed conversation")
	if err != nil {
		return s.WorkbenchState(), false, err
	}
	defer release()
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	activeProjectID, activeSessionID := s.activeSessionIDsLocked()
	if projectID != activeProjectID || sessionID != activeSessionID {
		return s.workbenchStateLocked(), false, nil
	}
	s.activateCurrent()
	return s.workbenchStateLocked(), true, nil
}

func cloneRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return settings
	}
	var cloned RuntimeSettings
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return settings
	}
	return cloned.Normalized()
}

func cloneDeepSeekState(state DeepSeekState) DeepSeekState {
	state.Models = append([]protocol.Model(nil), state.Models...)
	return state
}

// sessionProject is intentionally small and detached; it is useful to callers
// that need to validate a task target without changing the active session.
func (s *Service) sessionProject(sessionID string) (projectID string, session project.Session, err error) {
	return s.projectSession("", sessionID)
}

func (s *Service) projectSession(projectID, sessionID string) (resolvedProjectID string, session project.Session, err error) {
	s.stateMu.RLock()
	projects := s.projects
	s.stateMu.RUnlock()
	if projects == nil {
		return "", project.Session{}, fmt.Errorf("会话运行时不可用")
	}
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if projectID != "" {
		session, ok := projects.ProjectSession(projectID, sessionID)
		if !ok {
			return "", project.Session{}, fmt.Errorf("项目 %s 中不存在会话: %s", projectID, sessionID)
		}
		return projectID, session, nil
	}
	resolvedProjectID, session, ok := projects.FindSession(sessionID)
	if !ok {
		return "", project.Session{}, fmt.Errorf("会话不存在或 ID 不唯一: %s", sessionID)
	}
	return resolvedProjectID, session, nil
}
