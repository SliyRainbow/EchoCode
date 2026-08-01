package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/cache"
)

// 多项目 / 多会话对外接口。切换项目或会话时会换掉 eventStore 并重建 sessionMessages。

// ProjectInfo 是给前端的项目摘要。
type ProjectInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WorkspaceRoot string `json:"workspaceRoot"`
	Pinned        bool   `json:"pinned"`
	IsActive      bool   `json:"isActive"`
	SessionCount  int    `json:"sessionCount"`
}

// SessionInfo 是给前端的会话摘要。
type SessionInfo struct {
	ProjectID string `json:"projectId"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Archived  bool   `json:"archived"`
	IsActive  bool   `json:"isActive"`
}

// ListProjects 返回所有项目。
func (s *Service) ListProjects() []ProjectInfo {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projects == nil {
		return []ProjectInfo{}
	}
	m := s.projects.Snapshot()
	out := make([]ProjectInfo, 0, len(m.Projects))
	for _, p := range m.Projects {
		out = append(out, ProjectInfo{
			ID:            p.ID,
			Name:          p.Name,
			WorkspaceRoot: p.WorkspaceRoot,
			Pinned:        p.Pinned,
			IsActive:      p.ID == m.ActiveProjectID,
			SessionCount:  len(p.Sessions),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Pinned && !out[j].Pinned
	})
	return out
}

// GetProjectInfo returns one project without exposing the persistent store.
func (s *Service) GetProjectInfo(projectID string) (ProjectInfo, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projects == nil {
		return ProjectInfo{}, errProjectsDisabled()
	}
	p, ok := s.projects.Project(strings.TrimSpace(projectID))
	if !ok {
		return ProjectInfo{}, fmt.Errorf("项目不存在: %s", projectID)
	}
	activeProjectID, _ := s.projects.ActiveIDs()
	return ProjectInfo{
		ID:            p.ID,
		Name:          p.Name,
		WorkspaceRoot: p.WorkspaceRoot,
		Pinned:        p.Pinned,
		IsActive:      p.ID == activeProjectID,
		SessionCount:  len(p.Sessions),
	}, nil
}

// ListSessions 返回活动项目下的会话（含归档，前端自行筛选）。
func (s *Service) ListSessions() []SessionInfo {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projects == nil {
		return []SessionInfo{}
	}
	m := s.projects.Snapshot()
	var out []SessionInfo
	for _, p := range m.Projects {
		if p.ID != m.ActiveProjectID {
			continue
		}
		for _, sess := range p.Sessions {
			out = append(out, SessionInfo{
				ProjectID: p.ID,
				ID:        sess.ID,
				Title:     sess.Title,
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
				Archived:  sess.Archived,
				IsActive:  sess.ID == m.ActiveSessionID,
			})
		}
	}
	if out == nil {
		return []SessionInfo{}
	}
	return out
}

// ProjectNode 是给前端侧边栏树的一个项目节点（含它的会话）。
type ProjectNode struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	WorkspaceRoot string        `json:"workspaceRoot"`
	Pinned        bool          `json:"pinned"`
	IsActive      bool          `json:"isActive"`
	Sessions      []SessionInfo `json:"sessions"`
}

// GetProjectTree 返回所有项目连带各自的会话（Codex 式树状侧边栏数据源）。
// 每个项目的会话按更新时间倒序，归档的排在最后。
func (s *Service) GetProjectTree() []ProjectNode {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projects == nil {
		return []ProjectNode{}
	}
	m := s.projects.Snapshot()
	out := make([]ProjectNode, 0, len(m.Projects))
	for _, p := range m.Projects {
		node := ProjectNode{
			ID:            p.ID,
			Name:          p.Name,
			WorkspaceRoot: p.WorkspaceRoot,
			Pinned:        p.Pinned,
			IsActive:      p.ID == m.ActiveProjectID,
			Sessions:      make([]SessionInfo, 0, len(p.Sessions)),
		}
		for _, sess := range p.Sessions {
			node.Sessions = append(node.Sessions, SessionInfo{
				ProjectID: p.ID,
				ID:        sess.ID,
				Title:     sess.Title,
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
				Archived:  sess.Archived,
				IsActive:  p.ID == m.ActiveProjectID && sess.ID == m.ActiveSessionID,
			})
		}
		// 未归档在前、各自按更新时间倒序。
		sortSessions(node.Sessions)
		out = append(out, node)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Pinned && !out[j].Pinned
	})
	return out
}

func sortSessions(list []SessionInfo) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Archived != list[j].Archived {
			return !list[i].Archived // 未归档在前
		}
		return list[i].UpdatedAt > list[j].UpdatedAt // 新的在前
	})
}

// CreateProject 新建项目并切换过去（活动会话为新项目的初始会话）。
func (s *Service) CreateProject(name, workspaceRoot string) (WorkbenchState, error) {
	release, err := s.beginActivity("creating a project")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if strings.TrimSpace(name) == "" {
		name = defaultProjectName(workspaceRoot)
	}
	if _, err := s.projects.CreateProject(name, strings.TrimSpace(workspaceRoot), nil); err != nil {
		return s.workbenchStateLocked(), err
	}
	s.activateCurrent()
	return s.workbenchStateLocked(), nil
}

// SetProjectPinned changes project ordering in the sidebar.
func (s *Service) SetProjectPinned(projectID string, pinned bool) (WorkbenchState, error) {
	release, err := s.beginActivity("pinning a project")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if err := s.projects.SetProjectPinned(projectID, pinned); err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

// RenameProject changes the display name without renaming or moving the workspace.
func (s *Service) RenameProject(projectID, name string) (WorkbenchState, error) {
	release, err := s.beginActivity("renaming a project")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if err := s.projects.RenameProject(projectID, name); err != nil {
		return s.workbenchStateLocked(), err
	}
	s.refreshProjectMemory()
	return s.workbenchStateLocked(), nil
}

// ArchiveProjectTasks archives every existing session in a project. When the
// active project is archived, the store creates a fresh empty session.
func (s *Service) ArchiveProjectTasks(projectID string) (WorkbenchState, error) {
	release, err := s.beginActivity("archiving project tasks")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	activeChanged, err := s.projects.SetProjectSessionsArchived(projectID, true)
	if err != nil {
		return s.workbenchStateLocked(), err
	}
	if activeChanged {
		s.activateCurrent()
	} else {
		s.refreshProjectMemory()
	}
	return s.workbenchStateLocked(), nil
}

// RemoveProject hides a project while preserving its sessions and event logs.
// Adding the same workspace again restores the original project identity.
func (s *Service) RemoveProject(projectID string) (WorkbenchState, error) {
	release, err := s.beginActivity("removing a project")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	manifest := s.projects.Snapshot()
	var activeChanged bool
	if len(manifest.Projects) == 1 {
		temporaryRoot := strings.TrimSpace(s.config.TemporaryWorkspaceRoot)
		if temporaryRoot == "" {
			temporaryRoot = filepath.Join(os.TempDir(), "EchoCodeProject")
		}
		temporaryRoot, err = filepath.Abs(temporaryRoot)
		if err != nil {
			return s.workbenchStateLocked(), err
		}
		if err := os.MkdirAll(temporaryRoot, 0o755); err != nil {
			return s.workbenchStateLocked(), fmt.Errorf("创建临时工作区失败: %w", err)
		}
		if active, ok := s.projects.Project(projectID); ok && sameWorkspacePath(active.WorkspaceRoot, temporaryRoot) {
			temporaryRoot = filepath.Join(temporaryRoot, "Temporary")
			if err := os.MkdirAll(temporaryRoot, 0o755); err != nil {
				return s.workbenchStateLocked(), fmt.Errorf("创建备用临时工作区失败: %w", err)
			}
		}
		_, changed, removeErr := s.projects.RemoveProjectWithFallback(projectID, "EchoCodeProject", temporaryRoot, nil)
		if removeErr != nil {
			return s.workbenchStateLocked(), removeErr
		}
		activeChanged = changed
	} else {
		_, changed, removeErr := s.projects.RemoveProject(projectID)
		if removeErr != nil {
			return s.workbenchStateLocked(), removeErr
		}
		activeChanged = changed
	}
	if activeChanged {
		s.activateCurrent()
	} else {
		s.refreshProjectMemory()
	}
	return s.workbenchStateLocked(), nil
}

// OpenProjectInFileManager opens any configured project root in the host file manager.
func (s *Service) OpenProjectInFileManager(projectID string) error {
	s.stateMu.RLock()
	projects := s.projects
	openPath := s.config.OpenFile
	s.stateMu.RUnlock()
	if projects == nil {
		return errProjectsDisabled()
	}
	project, ok := projects.Project(strings.TrimSpace(projectID))
	if !ok {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	root, err := filepath.Abs(strings.TrimSpace(project.WorkspaceRoot))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("项目目录不存在或无法访问: %s", root)
	}
	if openPath == nil {
		return fmt.Errorf("当前桌面环境不支持打开项目目录")
	}
	return openPath(root)
}

// SwitchProject 切换活动项目。
func (s *Service) SwitchProject(projectID string) (WorkbenchState, error) {
	release, err := s.beginActivity("switching projects")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if err := s.projects.SwitchProject(projectID); err != nil {
		return s.workbenchStateLocked(), err
	}
	s.activateCurrent()
	return s.workbenchStateLocked(), nil
}

// NewSession 在活动项目下新建会话并切换过去。
func (s *Service) NewSession() (WorkbenchState, error) {
	release, err := s.beginActivity("starting a new session")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		// 无项目清单：退回旧的清空重开语义。
		s.resetDeepSeekSession("用户手动开启新会话。")
		return s.workbenchStateLocked(), nil
	}
	if _, err := s.projects.NewSession("新对话"); err != nil {
		return s.workbenchStateLocked(), err
	}
	s.activateCurrent()
	return s.workbenchStateLocked(), nil
}

// SwitchSession 切换活动会话。
func (s *Service) SwitchSession(sessionID string) (WorkbenchState, error) {
	projectID, _ := s.ActiveSessionIDs()
	return s.SwitchProjectSession(projectID, sessionID)
}

// SwitchProjectSession switches an exact project/session pair atomically.
func (s *Service) SwitchProjectSession(projectID, sessionID string) (WorkbenchState, error) {
	release, err := s.beginActivity("switching sessions")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if err := s.projects.SwitchProjectSession(projectID, sessionID); err != nil {
		return s.workbenchStateLocked(), err
	}
	s.activateCurrent()
	return s.workbenchStateLocked(), nil
}

// RenameSession updates a session title after resolving an unambiguous bare ID.
func (s *Service) RenameSession(sessionID, title string) (WorkbenchState, error) {
	projectID, _, err := s.sessionProject(sessionID)
	if err != nil {
		return s.WorkbenchState(), err
	}
	return s.RenameProjectSession(projectID, sessionID, title)
}

// RenameProjectSession updates only the exact project/session pair.
func (s *Service) RenameProjectSession(projectID, sessionID, title string) (WorkbenchState, error) {
	release, err := s.beginActivity("renaming a session")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return s.workbenchStateLocked(), fmt.Errorf("会话名称不能为空")
	}
	if err := s.projects.SetSessionTitleForProject(projectID, sessionID, title); err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

// ArchiveSession 归档 / 取消归档会话。
func (s *Service) ArchiveSession(sessionID string, archived bool) (WorkbenchState, error) {
	projectID, _, err := s.sessionProject(sessionID)
	if err != nil {
		return s.WorkbenchState(), err
	}
	return s.ArchiveProjectSession(projectID, sessionID, archived)
}

// ArchiveProjectSession archives an exact project/session pair.
func (s *Service) ArchiveProjectSession(projectID, sessionID string, archived bool) (WorkbenchState, error) {
	release, err := s.beginActivity("archiving a session")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	if err := s.projects.SetArchivedForProject(projectID, sessionID, archived); err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

// DeleteSession 永久删除会话元数据与对应事件日志目录。
func (s *Service) DeleteSession(sessionID string) (WorkbenchState, error) {
	projectID, _, err := s.sessionProject(sessionID)
	if err != nil {
		return s.WorkbenchState(), err
	}
	return s.DeleteProjectSession(projectID, sessionID)
}

// DeleteProjectSession permanently removes one exact project/session pair.
func (s *Service) DeleteProjectSession(projectID, sessionID string) (WorkbenchState, error) {
	release, err := s.beginActivity("deleting a session")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.projects == nil {
		return s.workbenchStateLocked(), errProjectsDisabled()
	}
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	activeChanged, err := s.projects.DeleteProjectSession(projectID, sessionID)
	if err != nil {
		return s.workbenchStateLocked(), err
	}

	var removeErr error
	if strings.TrimSpace(s.config.SessionsDir) != "" {
		var sessionDir string
		sessionDir, removeErr = safeSessionEventDir(s.config.SessionsDir, projectID, sessionID)
		if removeErr == nil {
			removeErr = os.RemoveAll(sessionDir)
		}
	}

	if activeChanged {
		s.activateCurrent()
	} else {
		s.refreshProjectMemory()
	}
	if removeErr != nil {
		return s.workbenchStateLocked(), fmt.Errorf("会话已从列表删除，但清理本地事件目录失败: %w", removeErr)
	}
	return s.workbenchStateLocked(), nil
}

func safeSessionEventDir(root, projectID, sessionID string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, projectID, sessionID))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("拒绝清理会话目录: %s", targetAbs)
	}
	return targetAbs, nil
}

func safeProjectEventDir(root, projectID string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, projectID))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("拒绝清理项目会话目录: %s", targetAbs)
	}
	return targetAbs, nil
}

// activateCurrent 依据项目清单的活动指针，重开事件日志并重建内存会话状态。
func (s *Service) activateCurrent() {
	s.approvals.resetSessionAllow()
	s.openActiveSession()
	// 清空内存会话，让下一轮 ensureProviderSession 重新初始化 system 前缀，
	// 再从事件日志重建历史消息。
	s.sessionMessages = nil
	s.lastRequest = nil
	s.metrics = cache.UsageMetrics{}
	s.metricsHistory = nil
	s.rebuildSessionFromEvents()
	s.restoreUsageMetrics()
	s.refreshProjectMemory()
}

func errProjectsDisabled() error {
	return &simpleError{msg: "未启用多项目（缺少 ProjectsPath）"}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// baseNameFromPath 取路径最后一段作为名称（跨分隔符）。
func baseNameFromPath(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `\/`)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	parts := strings.Split(p, "/")
	return parts[len(parts)-1]
}
