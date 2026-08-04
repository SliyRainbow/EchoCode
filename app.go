package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/appupdate"
	"github.com/SliyRainbow/EchoCode/internal/automation"
	"github.com/SliyRainbow/EchoCode/internal/browserengine"
	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
	"github.com/SliyRainbow/EchoCode/internal/extensions"
	"github.com/SliyRainbow/EchoCode/internal/storage"
	"github.com/SliyRainbow/EchoCode/internal/terminal"
	"github.com/SliyRainbow/EchoCode/internal/vault"
	"github.com/SliyRainbow/EchoCode/internal/workspacegit"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx               context.Context
	service           *agent.Service
	preview           *workspacePreviewServer
	browser           *browserengine.Service
	computer          *computercontrol.Service
	chat              chatTaskRunner
	terminal          *terminal.Manager
	git               workspacegit.Service
	updater           *appupdate.Service
	automations       *automation.Service
	extensions        *extensions.Service
	runtimeSettingsMu sync.RWMutex
	runtimeSettings   agent.RuntimeSettings
}

func NewApp() *App {
	usageStore, usageStoreErr := storage.Open(usageDatabasePath())
	usageStoreError := ""
	if usageStoreErr != nil {
		usageStoreError = usageStoreErr.Error()
	}
	automations, automationErr := automation.New(automationTasksPath())
	if automationErr != nil {
		automations, _ = automation.New("")
	}
	app := &App{
		preview:  newWorkspacePreviewServer(),
		browser:  browserengine.New(browserProfileDir(), browserDownloadsDir()),
		computer: computercontrol.New(),
		terminal: terminal.NewManager(),
		updater: appupdate.New(appupdate.Options{
			CurrentVersion: appVersion,
			Commit:         appCommit,
			BuildDate:      appBuildDate,
			CacheDir:       updateCacheDir(),
		}),
		automations: automations,
		extensions:  newExtensionService(),
	}
	app.service = agent.NewService(agent.ServiceConfig{
		AppVersion:             appVersion,
		SkillsDir:              "skills",
		UserSkillsDir:          userSkillsDir(),
		SkillsFS:               bundledSkills,
		SettingsPath:           runtimeSettingsPath(),
		SessionsDir:            sessionsDir(),
		ProjectsPath:           projectsPath(),
		TemporaryWorkspaceRoot: temporaryWorkspaceRoot(),
		Vault:                  vault.NewOSVault(),
		OpenFile:               openDesktopFile,
		PreviewFile:            app.previewFileFromAgent,
		RevealFile:             revealDesktopFile,
		Browser:                &browserToolBridge{app: app},
		Computer:               &computerToolBridge{app: app},
		ArtifactRenderer:       &artifactRenderBridge{app: app},
		Git:                    app.git,
		Terminal:               app.terminal,
		PluginsDir:             pluginsDir(),
		UsageStore:             usageStore,
		UsageStoreError:        usageStoreError,
	})
	if app.automations != nil {
		app.automations.SetRunner(func(task automation.Task) (string, error) {
			return app.startChatMessageForProjectSessionRouteRegistered(
				task.ProjectID,
				task.SessionID,
				task.Prompt,
				nil,
				task.ProviderID,
				task.ModelID,
				func(chatTaskID string) { app.automations.AttachChatTask(task.ID, chatTaskID) },
			)
		})
	}
	initialSettings := app.service.WorkbenchState().RuntimeSettings
	app.setRuntimeSettings(initialSettings)
	_ = app.configureBrowser(initialSettings)
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.automations != nil {
		a.automations.SetNotify(func(state automation.State) {
			wruntime.EventsEmit(ctx, "automation:state", state)
		})
		a.automations.Start(ctx)
	}
	if a.updater != nil {
		a.updater.SetNotify(func(state appupdate.State) {
			wruntime.EventsEmit(ctx, "update:state", state)
		})
		wruntime.EventsEmit(ctx, "update:state", a.updater.State())
		settings := a.runtimeSettingsSnapshot().Update
		if settings.AutoCheck {
			go a.checkForUpdatesOnStartup(ctx, settings.AutoDownload)
		}
	}
	if a.terminal != nil {
		a.terminal.SetNotify(func(state terminal.SessionState) {
			wruntime.EventsEmit(ctx, "terminal:update", state)
		})
	}
	// 注入审批通知：工具循环需审批时，向前端发 "approval:request" 事件。
	a.service.SetApprovalNotify(func(req agent.ApprovalRequest) {
		wruntime.EventsEmit(ctx, "approval:request", req)
	})
	go func() {
		connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		state := a.service.ConfigureMCP(connectCtx, "")
		wruntime.EventsEmit(ctx, "mcp:state", state)
	}()
}

func (a *App) shutdown(_ context.Context) {
	a.cancelAllChatTasks()
	if a.automations != nil {
		a.automations.SetNotify(nil)
		a.automations.Stop()
	}
	if a.updater != nil {
		a.updater.SetNotify(nil)
	}
	if a.terminal != nil {
		a.terminal.SetNotify(nil)
		a.terminal.Close()
	}
	clearDataPolicy := a.runtimeSettingsSnapshot().Browser.ClearDataPolicy
	a.service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if a.browser != nil {
		if strings.EqualFold(clearDataPolicy, "session") {
			_ = a.browser.ClearData(ctx)
		} else {
			_ = a.browser.Stop(ctx)
		}
	}
	if a.preview != nil {
		_ = a.preview.Close(ctx)
	}
}

func (a *App) GetWorkbenchState() agent.WorkbenchState {
	return a.service.WorkbenchState()
}

func (a *App) SetReasoningLevel(level string) (agent.WorkbenchState, error) {
	return a.service.SetReasoningLevel(agent.ReasoningLevel(level))
}

func (a *App) SaveDeepSeekAPIKey(apiKey string) (agent.WorkbenchState, error) {
	return a.service.SaveDeepSeekAPIKey(apiKey)
}

func (a *App) ClearDeepSeekAPIKey() (agent.WorkbenchState, error) {
	return a.service.ClearDeepSeekAPIKey()
}

func (a *App) TestDeepSeekConnection() (agent.WorkbenchState, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.service.TestDeepSeekConnection(ctx)
}

func (a *App) SendDeepSeekMessage(prompt string) (agent.ChatResult, error) {
	return a.SendChatMessage(prompt)
}

func (a *App) SendChatMessage(prompt string) (agent.ChatResult, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.service.SendChatMessage(ctx, prompt)
}

func (a *App) ResetDeepSeekSession() (agent.WorkbenchState, error) {
	return a.service.ResetDeepSeekSession()
}

func (a *App) SaveRuntimeSettings(settings agent.RuntimeSettings) (agent.WorkbenchState, error) {
	previousSettings := a.runtimeSettingsSnapshot()
	previousRoot := previousSettings.WorkspaceRoot
	state, err := a.service.SaveRuntimeSettings(settings)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		if a.terminal != nil && previousSettings.ShellAccess && (!state.RuntimeSettings.ShellAccess ||
			previousSettings.SandboxMode != state.RuntimeSettings.SandboxMode ||
			previousSettings.MaxCommandMemoryMB != state.RuntimeSettings.MaxCommandMemoryMB ||
			previousSettings.MaxCommandCPUPercent != state.RuntimeSettings.MaxCommandCPUPercent ||
			previousSettings.MaxCommandProcesses != state.RuntimeSettings.MaxCommandProcesses) {
			a.terminal.StopAll()
		}
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
		if configureErr := a.configureBrowser(state.RuntimeSettings); configureErr != nil {
			return state, configureErr
		}
		if strings.EqualFold(state.RuntimeSettings.Browser.ClearDataPolicy, "all") {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if clearErr := a.browser.ClearData(ctx); clearErr != nil {
				return state, clearErr
			}
		}
		mcpCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		state = a.service.ConfigureMCP(mcpCtx, "")
		cancel()
	}
	return state, err
}

func (a *App) setRuntimeSettings(settings agent.RuntimeSettings) {
	a.runtimeSettingsMu.Lock()
	a.runtimeSettings = settings
	a.runtimeSettingsMu.Unlock()
}

func (a *App) runtimeSettingsSnapshot() agent.RuntimeSettings {
	a.runtimeSettingsMu.RLock()
	defer a.runtimeSettingsMu.RUnlock()
	return a.runtimeSettings
}

func (a *App) RefreshMCPServer(serverID string) agent.WorkbenchState {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.service.ConfigureMCP(ctx, serverID)
}

func (a *App) RefreshPlugins() agent.WorkbenchState {
	return a.service.RefreshPlugins()
}

func (a *App) SelectPluginDirectory() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "选择 EchoCode 插件目录"})
}

func (a *App) InstallPlugin(source string) (agent.WorkbenchState, error) {
	state, err := a.service.InstallPlugin(source)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
	}
	return state, err
}

func (a *App) UninstallPlugin(id string) (agent.WorkbenchState, error) {
	state, err := a.service.UninstallPlugin(id)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
	}
	return state, err
}

func (a *App) RevealPlugin(id string) error {
	return a.service.RevealPlugin(id)
}

func (a *App) configureBrowser(settings agent.RuntimeSettings) error {
	if a.browser == nil {
		return nil
	}
	permissions := make([]browserengine.SitePermission, 0, len(settings.Browser.SitePermissions))
	for _, permission := range settings.Browser.SitePermissions {
		permissions = append(permissions, browserengine.SitePermission{
			Origin:     permission.Origin,
			Camera:     permission.Camera,
			Microphone: permission.Microphone,
			Clipboard:  permission.Clipboard,
		})
	}
	return a.browser.Configure(browserengine.Settings{
		Enabled:                settings.Browser.Enabled,
		AllowNetwork:           settings.NetworkAccess,
		NativePresentation:     true,
		ClearDataPolicy:        settings.Browser.ClearDataPolicy,
		ScreenshotAnnotations:  settings.Browser.ScreenshotAnnotations,
		PasswordManagerEnabled: settings.Browser.PasswordManagerEnabled,
		AutofillContactEnabled: settings.Browser.AutofillContactEnabled,
		DeveloperCDPAccess:     settings.Browser.DeveloperCDPAccess,
		SitePermissions:        permissions,
		AutofillProfile: browserengine.AutofillProfile{
			FullName:      settings.Browser.AutofillProfile.FullName,
			Email:         settings.Browser.AutofillProfile.Email,
			Phone:         settings.Browser.AutofillProfile.Phone,
			Organization:  settings.Browser.AutofillProfile.Organization,
			StreetAddress: settings.Browser.AutofillProfile.StreetAddress,
			City:          settings.Browser.AutofillProfile.City,
			Region:        settings.Browser.AutofillProfile.Region,
			PostalCode:    settings.Browser.AutofillProfile.PostalCode,
			Country:       settings.Browser.AutofillProfile.Country,
		},
	})
}

func (a *App) SaveModelProviderAPIKey(providerID string, apiKey string) (agent.WorkbenchState, error) {
	return a.service.SaveModelProviderAPIKey(providerID, apiKey)
}

func (a *App) ClearModelProviderAPIKey(providerID string) (agent.WorkbenchState, error) {
	return a.service.ClearModelProviderAPIKey(providerID)
}

func (a *App) DeleteModelProvider(providerID string) (agent.WorkbenchState, error) {
	return a.service.DeleteModelProvider(providerID)
}

func (a *App) RefreshModelProviderModels(providerID string) (agent.WorkbenchState, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.service.RefreshModelProviderModels(ctx, providerID)
}

func (a *App) SaveModelProviderBillingAPIKey(providerID string, apiKey string) (agent.WorkbenchState, error) {
	return a.service.SaveModelProviderBillingAPIKey(providerID, apiKey)
}

func (a *App) ClearModelProviderBillingAPIKey(providerID string) (agent.WorkbenchState, error) {
	return a.service.ClearModelProviderBillingAPIKey(providerID)
}

func (a *App) GetUsageBillingReport(providerID string) (agent.UsageBillingReport, error) {
	return a.service.UsageBillingReport(providerID)
}

func (a *App) ReconcileUsageBilling(input agent.UsageBillingReconciliationInput) (agent.UsageBillingReport, error) {
	return a.service.ReconcileUsageBilling(input)
}

func (a *App) SyncUsageBilling(input agent.UsageBillingSyncInput) (agent.UsageBillingReport, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.service.SyncUsageBilling(ctx, input)
}

// ListCheckpoints 返回当前会话的可回退检查点（供前端 Timeline）。
func (a *App) ListCheckpoints() []agent.CheckpointInfo {
	return a.service.ListCheckpoints()
}

// RewindToCheckpoint 回退到指定检查点：对话与文件一起回退。
func (a *App) RewindToCheckpoint(checkpointID string) (agent.WorkbenchState, error) {
	return a.service.RewindToCheckpoint(checkpointID)
}

// ListBranches 返回所有对话线（分支）。
func (a *App) ListBranches() []agent.BranchInfo {
	return a.service.ListBranches()
}

// SwitchBranch 切换到另一条对话线（叶子），文件与对话一起切换。
func (a *App) SwitchBranch(leafID string) (agent.WorkbenchState, error) {
	return a.service.SwitchBranch(leafID)
}

// ForkFromMessage 从指定历史消息创建一条保留旧对话的新分支。
func (a *App) ForkFromMessage(messageEventID string) (agent.WorkbenchState, error) {
	projectID, sessionID := a.service.ActiveSessionIDs()
	return a.ForkFromMessageForProjectSession(projectID, sessionID, messageEventID)
}

// ForkFromMessageForProjectSession pins the rewind to the conversation that
// supplied the message. Background tasks in other conversations may continue.
func (a *App) ForkFromMessageForProjectSession(projectID, sessionID, messageEventID string) (agent.WorkbenchState, error) {
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	if err := a.requireProjectSessionIdleChat(projectID, sessionID); err != nil {
		return a.service.WorkbenchState(), err
	}
	return a.service.ForkFromMessageForProjectSession(projectID, sessionID, messageEventID)
}

// RespondApproval 由前端在用户点击审批弹窗后调用。
func (a *App) RespondApproval(id string, tool string, approved bool, scope string) error {
	if runtime := a.approvalService(id); runtime != nil {
		return runtime.RespondApproval(id, tool, approved, scope)
	}
	return a.service.RespondApproval(id, tool, approved, scope)
}

// SetPlanMode 开关 Plan 两段式（先规划后执行）。
func (a *App) SetPlanMode(enabled bool) agent.WorkbenchState {
	return a.service.SetPlanMode(enabled)
}

// --- 多项目 / 多会话 ---

func (a *App) ListProjects() []agent.ProjectInfo { return a.service.ListProjects() }
func (a *App) ListSessions() []agent.SessionInfo { return a.service.ListSessions() }

// GetProjectTree 返回所有项目连带各自会话（Codex 式树状侧边栏）。
func (a *App) GetProjectTree() []agent.ProjectNode { return a.service.GetProjectTree() }

// GetSessionMessages 返回当前活动会话的历史消息（供启动/切换会话时恢复对话）。
func (a *App) GetSessionMessages() []agent.SessionMessage {
	return a.service.GetSessionMessages()
}

func (a *App) GetSessionMessagesForSession(sessionID string) []agent.SessionMessage {
	return a.service.GetSessionMessagesForSession(sessionID)
}

func (a *App) GetSessionMessagesForProjectSession(projectID, sessionID string) ([]agent.SessionMessage, error) {
	return a.service.GetSessionMessagesForProjectSession(projectID, sessionID)
}

func (a *App) CreateProject(name string, workspaceRoot string) (agent.WorkbenchState, error) {
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.CreateProject(name, workspaceRoot)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	}
	return state, err
}
func (a *App) SwitchProject(projectID string) (agent.WorkbenchState, error) {
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.SwitchProject(projectID)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	}
	return state, err
}
func (a *App) SetProjectPinned(projectID string, pinned bool) (agent.WorkbenchState, error) {
	return a.service.SetProjectPinned(projectID, pinned)
}
func (a *App) RenameProject(projectID string, name string) (agent.WorkbenchState, error) {
	return a.service.RenameProject(projectID, name)
}
func (a *App) ArchiveProjectTasks(projectID string) (agent.WorkbenchState, error) {
	if err := a.requireIdleChat("归档项目任务"); err != nil {
		return a.service.WorkbenchState(), err
	}
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.ArchiveProjectTasks(projectID)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	}
	return state, err
}
func (a *App) RemoveProject(projectID string) (agent.WorkbenchState, error) {
	if err := a.requireIdleChat("移除项目"); err != nil {
		return a.service.WorkbenchState(), err
	}
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.RemoveProject(projectID)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	}
	return state, err
}
func (a *App) OpenProjectInFileManager(projectID string) error {
	return a.service.OpenProjectInFileManager(projectID)
}
func (a *App) NewSession() (agent.WorkbenchState, error) {
	return a.service.NewSession()
}
func (a *App) SwitchSession(sessionID string) (agent.WorkbenchState, error) {
	return a.service.SwitchSession(sessionID)
}
func (a *App) SwitchProjectSession(projectID, sessionID string) (agent.WorkbenchState, error) {
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.SwitchProjectSession(projectID, sessionID)
	if err == nil {
		a.setRuntimeSettings(state.RuntimeSettings)
		a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	}
	return state, err
}
func (a *App) RenameSession(sessionID, title string) (agent.WorkbenchState, error) {
	return a.service.RenameSession(sessionID, title)
}
func (a *App) RenameProjectSession(projectID, sessionID, title string) (agent.WorkbenchState, error) {
	return a.service.RenameProjectSession(projectID, sessionID, title)
}
func (a *App) ArchiveSession(sessionID string, archived bool) (agent.WorkbenchState, error) {
	return a.service.ArchiveSession(sessionID, archived)
}
func (a *App) ArchiveProjectSession(projectID, sessionID string, archived bool) (agent.WorkbenchState, error) {
	return a.service.ArchiveProjectSession(projectID, sessionID, archived)
}
func (a *App) DeleteSession(sessionID string) (agent.WorkbenchState, error) {
	if err := a.requireIdleChat("删除会话"); err != nil {
		return a.service.WorkbenchState(), err
	}
	return a.service.DeleteSession(sessionID)
}
func (a *App) DeleteProjectSession(projectID, sessionID string) (agent.WorkbenchState, error) {
	if err := a.requireProjectSessionIdleChat(projectID, sessionID); err != nil {
		return a.service.WorkbenchState(), err
	}
	return a.service.DeleteProjectSession(projectID, sessionID)
}

// SelectDirectory 弹出系统目录选择框，供"添加项目"选工作区根。
func (a *App) SelectDirectory() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "选择项目工作区目录",
	})
}

func (a *App) SelectWorktreeParentDirectory() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "选择永久工作树的父目录",
	})
}

// OpenWorkspaceFile uses the operating system's default application after the
// agent service verifies that the artifact belongs to the active workspace.
func (a *App) OpenWorkspaceFile(path string) error {
	return a.service.OpenWorkspaceFile(path)
}

// ReadWorkspaceFile returns a bounded, encoding-safe text preview for the
// right-side file panel. The agent service enforces the active workspace.
func (a *App) ReadWorkspaceFile(path string) (agent.WorkspaceFilePreview, error) {
	return a.service.ReadWorkspaceFile(path)
}

// ReadSkillDetail returns the selected Skill's bounded SKILL.md source for
// the settings viewer. The agent service resolves the name against indexed
// Skill roots before reading it.
func (a *App) ReadSkillDetail(name string) (agent.SkillDetail, error) {
	return a.service.ReadSkillDetail(name)
}

// ImportSkillMarkdown installs a user-authored Markdown file into EchoCode's
// durable per-user Skills directory and returns the refreshed workbench state.
func (a *App) ImportSkillMarkdown(fileName, content string) (agent.SkillImportResult, error) {
	return a.service.ImportSkillMarkdown(fileName, content)
}

// OpenSkillFile opens a disk-backed Skill source through the host application.
func (a *App) OpenSkillFile(name string) error {
	return a.service.OpenSkillFile(name)
}

// RevealSkillFile selects a disk-backed Skill source in the host file manager.
func (a *App) RevealSkillFile(name string) error {
	return a.service.RevealSkillFile(name)
}

// ListWorkspaceDirectory returns one project-scoped directory level for the
// right-side file explorer.
func (a *App) ListWorkspaceDirectory(path string) (agent.WorkspaceDirectoryListing, error) {
	return a.service.ListWorkspaceDirectory(path)
}

// PreviewWorkspaceFile returns a loopback URL for the embedded browser. The
// service validates the requested file against the active workspace first.
func (a *App) PreviewWorkspaceFile(path string) (BrowserPreview, error) {
	abs, err := a.service.ResolveWorkspaceFile(path)
	if err != nil {
		return BrowserPreview{}, err
	}
	if !isHTMLFile(abs) {
		return BrowserPreview{}, fmt.Errorf("内置预览当前仅支持 HTML 文件")
	}
	settings := a.runtimeSettingsSnapshot()
	if !settings.Browser.Enabled {
		return BrowserPreview{}, fmt.Errorf("内置浏览器已在设置中关闭")
	}
	return a.previewResolvedWorkspaceFile(abs)
}

// RevealWorkspaceFile selects an artifact in the operating system file manager.
func (a *App) RevealWorkspaceFile(path string) error {
	return a.service.RevealWorkspaceFile(path)
}

func (a *App) previewFileFromAgent(path string) error {
	if !isHTMLFile(path) {
		return openDesktopFile(path)
	}
	settings := a.runtimeSettingsSnapshot().Browser
	if !settings.Enabled || strings.EqualFold(settings.DefaultLocalURLDestination, "system") {
		return openDesktopFile(path)
	}
	preview, err := a.previewResolvedWorkspaceFile(path)
	if err != nil {
		return err
	}
	preview.Ask = strings.EqualFold(settings.DefaultLocalURLDestination, "ask")
	if a.ctx == nil {
		return fmt.Errorf("EchoCode 前端尚未就绪")
	}
	wruntime.EventsEmit(a.ctx, "browser:open", preview)
	return nil
}

func (a *App) previewResolvedWorkspaceFile(path string) (BrowserPreview, error) {
	if a.preview == nil {
		return BrowserPreview{}, fmt.Errorf("内置浏览器预览服务不可用")
	}
	workspaceRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	preview, err := a.preview.Preview(workspaceRoot, path)
	if err != nil {
		return BrowserPreview{}, err
	}
	if a.browser != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		state, browserErr := a.browser.Open(ctx, preview.URL)
		cancel()
		if browserErr == nil {
			preview.TabID = state.ActiveTabID
			preview.Managed = true
		}
	}
	return preview, nil
}

func (a *App) resetPreviewIfWorkspaceChanged(previousRoot, nextRoot string) {
	if filepath.Clean(strings.TrimSpace(previousRoot)) == filepath.Clean(strings.TrimSpace(nextRoot)) {
		return
	}
	a.preview.Reset()
	if a.terminal != nil {
		a.terminal.StopAll()
	}
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, "browser:close")
	}
}

func runtimeSettingsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode.runtime.json"
	}
	return filepath.Join(configDir, "EchoCode", "runtime-settings.json")
}

// sessionsDir 返回会话事件日志的根目录。
func sessionsDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode-sessions"
	}
	return filepath.Join(configDir, "EchoCode", "sessions")
}

// projectsPath 返回项目清单 JSON 路径。
func projectsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode-projects.json"
	}
	return filepath.Join(configDir, "EchoCode", "projects.json")
}

func automationTasksPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode-automations.json"
	}
	return filepath.Join(configDir, "EchoCode", "automations.json")
}

func pluginsDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode-plugins"
	}
	return filepath.Join(configDir, "EchoCode", "plugins")
}

func userSkillsDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return filepath.Join(".echocode", "skills")
	}
	return filepath.Join(configDir, "EchoCode", "skills")
}

func temporaryWorkspaceRoot() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, "EchoCodeProject")
	}
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, "EchoCode", "EchoCodeProject")
	}
	return filepath.Join(".", "EchoCodeProject")
}

func usageDatabasePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "echocode.db"
	}
	return filepath.Join(configDir, "EchoCode", "echocode.db")
}

func updateCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		return filepath.Join(os.TempDir(), "EchoCode", "updates")
	}
	return filepath.Join(cacheDir, "EchoCode", "updates")
}

func visualRenderCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		return filepath.Join(os.TempDir(), "EchoCode", "visual-renders")
	}
	return filepath.Join(cacheDir, "EchoCode", "visual-renders")
}

func webviewUserDataDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		return filepath.Join(os.TempDir(), "EchoCode", "WebView2")
	}
	return filepath.Join(cacheDir, "EchoCode", "WebView2")
}

func browserProfileDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return filepath.Join(".echocode", "browser-profile")
	}
	return filepath.Join(configDir, "EchoCode", "browser-profile")
}

func browserDownloadsDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return filepath.Join(".echocode", "downloads")
	}
	return filepath.Join(homeDir, "Downloads", "EchoCode")
}
