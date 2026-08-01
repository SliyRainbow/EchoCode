package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
	"github.com/SliyRainbow/EchoCode/internal/terminal"
	"github.com/SliyRainbow/EchoCode/internal/workspacegit"
)

func (a *App) GetGitStatus() (workspacegit.Status, error) {
	settings := a.runtimeSettingsSnapshot()
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.Status(ctx, settings.WorkspaceRoot)
}

func (a *App) GetGitDiff(path string, staged bool) (workspacegit.Diff, error) {
	settings := a.runtimeSettingsSnapshot()
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.Diff(ctx, settings.WorkspaceRoot, path, staged)
}

func (a *App) GetGitReviewDiff(path string, staged, ignoreWhitespace bool) (workspacegit.Diff, error) {
	settings := a.runtimeSettingsSnapshot()
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.DiffWithOptions(ctx, settings.WorkspaceRoot, path, workspacegit.DiffOptions{
		Staged:           staged,
		IgnoreWhitespace: ignoreWhitespace,
	})
}

func (a *App) StageGitPaths(paths []string) (workspacegit.Status, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return workspacegit.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.Stage(ctx, settings.WorkspaceRoot, paths)
}

func (a *App) UnstageGitPaths(paths []string) (workspacegit.Status, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return workspacegit.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.Unstage(ctx, settings.WorkspaceRoot, paths)
}

func (a *App) CommitGitChanges(message string) (workspacegit.Status, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return workspacegit.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 60*time.Second)
	defer cancel()
	return a.git.Commit(ctx, settings.WorkspaceRoot, message)
}

func (a *App) CreateGitBranch(name string) (workspacegit.Status, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return workspacegit.Status{}, err
	}
	name = strings.TrimSpace(name)
	name = applyGitBranchPrefix(settings, name)
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.CreateBranch(ctx, settings.WorkspaceRoot, name)
}

// CreatePermanentWorktree creates a linked Git worktree and registers it as a new project.
func (a *App) CreatePermanentWorktree(projectID, branchName, destination string) (agent.WorkbenchState, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return a.service.WorkbenchState(), err
	}
	project, err := a.service.GetProjectInfo(projectID)
	if err != nil {
		return a.service.WorkbenchState(), err
	}
	branchName = applyGitBranchPrefix(settings, branchName)
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 2*time.Minute+5*time.Second)
	defer cancel()
	worktree, err := a.git.CreateWorktree(ctx, project.WorkspaceRoot, branchName, destination)
	if err != nil {
		return a.service.WorkbenchState(), err
	}
	name := filepath.Base(filepath.Clean(worktree.Path))
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = worktree.Branch
	}
	previousRoot := a.runtimeSettingsSnapshot().WorkspaceRoot
	state, err := a.service.CreateProject(name, worktree.Path)
	if err != nil {
		return state, fmt.Errorf("永久工作树已创建于 %s，但加入项目列表失败: %w", worktree.Path, err)
	}
	a.setRuntimeSettings(state.RuntimeSettings)
	a.resetPreviewIfWorkspaceChanged(previousRoot, state.RuntimeSettings.WorkspaceRoot)
	return state, nil
}

func (a *App) SwitchGitBranch(name string) (workspacegit.Status, error) {
	settings, err := a.gitWriteSettings()
	if err != nil {
		return workspacegit.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.workspaceToolsContext(), 30*time.Second)
	defer cancel()
	return a.git.SwitchBranch(ctx, settings.WorkspaceRoot, name)
}

func (a *App) StartTerminalSession() (terminal.SessionState, error) {
	if err := a.requireIdleChat("启动新的终端会话"); err != nil {
		return terminal.SessionState{}, err
	}
	settings := a.runtimeSettingsSnapshot()
	if !settings.ShellAccess {
		return terminal.SessionState{}, errors.New("Shell access is disabled in settings")
	}
	if a.terminal == nil {
		return terminal.SessionState{}, errors.New("terminal service is unavailable")
	}
	return a.terminal.StartWithLimits(settings.WorkspaceRoot, sandboxexec.Limits{
		MemoryBytes:        uint64(settings.MaxCommandMemoryMB) * 1024 * 1024,
		CPUPercent:         uint32(settings.MaxCommandCPUPercent),
		MaxProcesses:       uint32(settings.MaxCommandProcesses),
		RestrictPrivileges: !strings.EqualFold(settings.SandboxMode, "danger-full-access"),
	})
}

func (a *App) GetTerminalSession(sessionID string) (terminal.SessionState, error) {
	if a.terminal == nil {
		return terminal.SessionState{}, errors.New("terminal service is unavailable")
	}
	return a.terminal.State(sessionID)
}

func (a *App) SendTerminalCommand(sessionID, command string) error {
	settings := a.runtimeSettingsSnapshot()
	if !settings.ShellAccess {
		return errors.New("Shell access is disabled in settings")
	}
	if a.terminal == nil {
		return errors.New("terminal service is unavailable")
	}
	return a.terminal.WriteLine(sessionID, command)
}

func (a *App) StopTerminalSession(sessionID string) error {
	if a.terminal == nil {
		return nil
	}
	return a.terminal.Stop(sessionID)
}

func (a *App) gitWriteSettings() (agent.RuntimeSettings, error) {
	if err := a.requireIdleChat("执行 Git 写操作"); err != nil {
		return agent.RuntimeSettings{}, err
	}
	settings := a.runtimeSettingsSnapshot()
	if strings.EqualFold(settings.SandboxMode, "read-only") || strings.EqualFold(settings.FilesystemAccess, "read-only") {
		return settings, errors.New("Git write operations are disabled in read-only mode")
	}
	return settings, nil
}

func applyGitBranchPrefix(settings agent.RuntimeSettings, name string) string {
	name = strings.TrimSpace(name)
	prefix := strings.TrimSpace(settings.Git.BranchPrefix)
	if prefix != "" && name != "" && !strings.HasPrefix(name, prefix) {
		name = strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(name, "/")
	}
	return name
}

func (a *App) workspaceToolsContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
