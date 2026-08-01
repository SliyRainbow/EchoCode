// Package project 管理多项目与多会话的元数据，持久化到单个 JSON 文件（projects.json）。
// 会话的实际内容仍由 eventlog 存储（每会话一个目录）；本包只管「有哪些项目/会话、
// 哪个是活动的、标题/归档/时间」等元信息。不依赖 SQLite。
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// Session 是一个会话的元数据。实际对话事件存于 eventlog 目录。
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Archived  bool   `json:"archived"`
}

// Project 是一个项目：一个工作区根 + 其下的一组会话。
type Project struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	WorkspaceRoot       string    `json:"workspaceRoot"`
	ExtraWritableRoots  []string  `json:"extraWritableRoots"`
	CreatedAt           string    `json:"createdAt"`
	Pinned              bool      `json:"pinned,omitempty"`
	LastActiveSessionID string    `json:"lastActiveSessionId,omitempty"`
	Sessions            []Session `json:"sessions"`
}

// Manifest 是整个持久化文件的根结构。
type Manifest struct {
	Projects         []Project `json:"projects"`
	DetachedProjects []Project `json:"detachedProjects,omitempty"`
	ActiveProjectID  string    `json:"activeProjectId"`
	ActiveSessionID  string    `json:"activeSessionId"`
}

// Store 读写 projects.json，并发安全。
type Store struct {
	mu       sync.Mutex
	path     string
	manifest Manifest
}

// Open 打开（或初始化）指定路径的项目清单。
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.manifest = Manifest{}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.manifest)
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.manifest, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteBytesAtomic(s.path, data, 0o600)
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func genID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// EnsureBootstrap 保证至少有一个项目与一个活动会话。
// 首次运行时用给定的默认工作区根创建默认项目。返回是否新建了内容。
func (s *Store) EnsureBootstrap(defaultWorkspaceRoot, defaultProjectName string) (created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.manifest.Projects) == 0 {
		sess := newSession("新对话")
		proj := Project{
			ID:                  genID("proj"),
			Name:                defaultProjectName,
			WorkspaceRoot:       defaultWorkspaceRoot,
			CreatedAt:           nowRFC3339(),
			LastActiveSessionID: sess.ID,
			Sessions:            []Session{sess},
		}
		s.manifest.Projects = []Project{proj}
		s.manifest.ActiveProjectID = proj.ID
		s.manifest.ActiveSessionID = sess.ID
		return true, s.save()
	}

	changed := false
	// 已有项目但活动指针缺失 → 修正。
	if s.findProject(s.manifest.ActiveProjectID) == nil {
		s.manifest.ActiveProjectID = s.manifest.Projects[0].ID
		changed = true
	}
	active := s.findProject(s.manifest.ActiveProjectID)
	if len(active.Sessions) == 0 {
		sess := newSession("新对话")
		active.Sessions = append(active.Sessions, sess)
		s.manifest.ActiveSessionID = sess.ID
		active.LastActiveSessionID = sess.ID
		return true, s.save()
	}
	if active.findSession(s.manifest.ActiveSessionID) == nil {
		s.manifest.ActiveSessionID = ensureProjectActiveSession(active)
		changed = true
	}
	if active.LastActiveSessionID != s.manifest.ActiveSessionID {
		active.LastActiveSessionID = s.manifest.ActiveSessionID
		changed = true
	}
	if changed {
		return true, s.save()
	}
	return false, nil
}

func newSession(title string) Session {
	now := nowRFC3339()
	return Session{ID: genID("sess"), Title: title, CreatedAt: now, UpdatedAt: now}
}

func (s *Store) findProject(id string) *Project {
	for i := range s.manifest.Projects {
		if s.manifest.Projects[i].ID == id {
			return &s.manifest.Projects[i]
		}
	}
	return nil
}

func (p *Project) findSession(id string) *Session {
	for i := range p.Sessions {
		if p.Sessions[i].ID == id {
			return &p.Sessions[i]
		}
	}
	return nil
}

// Snapshot 返回清单的只读副本（供上层查询，不暴露内部指针）。
func (s *Store) Snapshot() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneManifest(s.manifest)
}

func cloneManifest(m Manifest) Manifest {
	out := Manifest{ActiveProjectID: m.ActiveProjectID, ActiveSessionID: m.ActiveSessionID}
	for _, p := range m.Projects {
		cp := p
		cp.ExtraWritableRoots = append([]string{}, p.ExtraWritableRoots...)
		cp.Sessions = append([]Session{}, p.Sessions...)
		out.Projects = append(out.Projects, cp)
	}
	for _, p := range m.DetachedProjects {
		cp := p
		cp.ExtraWritableRoots = append([]string{}, p.ExtraWritableRoots...)
		cp.Sessions = append([]Session{}, p.Sessions...)
		out.DetachedProjects = append(out.DetachedProjects, cp)
	}
	return out
}

// ActiveProject 返回当前活动项目（副本）。
func (s *Store) ActiveProject() (Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(s.manifest.ActiveProjectID)
	if p == nil {
		return Project{}, false
	}
	return cloneManifest(Manifest{Projects: []Project{*p}}).Projects[0], true
}

// Project returns a project by ID as a detached copy.
func (s *Store) Project(projectID string) (Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(strings.TrimSpace(projectID))
	if p == nil {
		return Project{}, false
	}
	return cloneManifest(Manifest{Projects: []Project{*p}}).Projects[0], true
}

// FindSession returns the project and detached session that owns sessionID.
// It does not change the active project/session pointers, which is important
// while background Agent tasks are running for more than one conversation.
func (s *Store) FindSession(sessionID string) (projectID string, session Session, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var foundProjectID string
	var foundSession Session
	found := false
	for _, project := range s.manifest.Projects {
		for _, candidate := range project.Sessions {
			if candidate.ID == strings.TrimSpace(sessionID) {
				if found {
					// A bare session ID is ambiguous. Callers that know the project
					// must use ProjectSession instead of silently selecting one.
					return "", Session{}, false
				}
				foundProjectID, foundSession, found = project.ID, candidate, true
			}
		}
	}
	return foundProjectID, foundSession, found
}

// ProjectSession resolves a session only inside its owning project.
func (s *Store) ProjectSession(projectID, sessionID string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.findProject(strings.TrimSpace(projectID))
	if project == nil {
		return Session{}, false
	}
	session := project.findSession(strings.TrimSpace(sessionID))
	if session == nil {
		return Session{}, false
	}
	return *session, true
}

// ActiveIDs 返回当前活动的项目 ID 与会话 ID。
func (s *Store) ActiveIDs() (projectID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifest.ActiveProjectID, s.manifest.ActiveSessionID
}

// CreateProject 新建项目（自动带一个初始会话）并切为活动。返回新项目。
func (s *Store) CreateProject(name, workspaceRoot string, extraRoots []string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := cloneManifest(s.manifest)
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	s.rememberActiveSession()
	for index := range s.manifest.Projects {
		project := &s.manifest.Projects[index]
		if !sameWorkspaceRoot(project.WorkspaceRoot, workspaceRoot) {
			continue
		}
		s.manifest.ActiveProjectID = project.ID
		s.manifest.ActiveSessionID = ensureProjectActiveSession(project)
		if err := s.save(); err != nil {
			s.manifest = original
			return Project{}, err
		}
		return cloneManifest(Manifest{Projects: []Project{*project}}).Projects[0], nil
	}
	for index := range s.manifest.DetachedProjects {
		project := s.manifest.DetachedProjects[index]
		if !sameWorkspaceRoot(project.WorkspaceRoot, workspaceRoot) {
			continue
		}
		s.manifest.DetachedProjects = append(s.manifest.DetachedProjects[:index], s.manifest.DetachedProjects[index+1:]...)
		if strings.TrimSpace(project.Name) == "" {
			project.Name = name
		}
		project.WorkspaceRoot = workspaceRoot
		if len(extraRoots) > 0 {
			project.ExtraWritableRoots = append([]string(nil), extraRoots...)
		}
		if len(project.Sessions) == 0 {
			project.Sessions = []Session{newSession("新对话")}
		}
		s.manifest.Projects = append(s.manifest.Projects, project)
		restored := &s.manifest.Projects[len(s.manifest.Projects)-1]
		s.manifest.ActiveProjectID = restored.ID
		s.manifest.ActiveSessionID = ensureProjectActiveSession(restored)
		if err := s.save(); err != nil {
			s.manifest = original
			return Project{}, err
		}
		return cloneManifest(Manifest{Projects: []Project{*restored}}).Projects[0], nil
	}
	sess := newSession("新对话")
	proj := Project{
		ID:                  genID("proj"),
		Name:                name,
		WorkspaceRoot:       workspaceRoot,
		ExtraWritableRoots:  extraRoots,
		CreatedAt:           nowRFC3339(),
		LastActiveSessionID: sess.ID,
		Sessions:            []Session{sess},
	}
	s.manifest.Projects = append(s.manifest.Projects, proj)
	s.manifest.ActiveProjectID = proj.ID
	s.manifest.ActiveSessionID = sess.ID
	if err := s.save(); err != nil {
		s.manifest = original
		return Project{}, err
	}
	return proj, nil
}

func sameWorkspaceRoot(left, right string) bool {
	left = canonicalWorkspaceRoot(left)
	right = canonicalWorkspaceRoot(right)
	if left == "" || right == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func canonicalWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	root = filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = filepath.Clean(resolved)
	}
	return root
}

// SetProjectPinned persists whether a project should be sorted before regular projects.
func (s *Store) SetProjectPinned(projectID string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(strings.TrimSpace(projectID))
	if p == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	if p.Pinned == pinned {
		return nil
	}
	original := p.Pinned
	p.Pinned = pinned
	if err := s.save(); err != nil {
		p.Pinned = original
		return err
	}
	return nil
}

// RenameProject updates only the display name; the workspace path is unchanged.
func (s *Store) RenameProject(projectID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("项目名称不能为空")
	}
	if len(name) > 200 {
		return fmt.Errorf("项目名称过长")
	}
	p := s.findProject(strings.TrimSpace(projectID))
	if p == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	if p.Name == name {
		return nil
	}
	original := p.Name
	p.Name = name
	if err := s.save(); err != nil {
		p.Name = original
		return err
	}
	return nil
}

// SetProjectSessionsArchived archives or restores every existing task in a project.
// Archiving the active project creates a fresh task so the active pointer never targets
// a hidden archived session.
func (s *Store) SetProjectSessionsArchived(projectID string, archived bool) (activeChanged bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := cloneManifest(s.manifest)
	p := s.findProject(strings.TrimSpace(projectID))
	if p == nil {
		return false, fmt.Errorf("项目不存在: %s", projectID)
	}
	now := nowRFC3339()
	for i := range p.Sessions {
		p.Sessions[i].Archived = archived
		p.Sessions[i].UpdatedAt = now
	}
	if archived && p.ID == s.manifest.ActiveProjectID {
		replacement := newSession("新对话")
		p.Sessions = append(p.Sessions, replacement)
		s.manifest.ActiveSessionID = replacement.ID
		p.LastActiveSessionID = replacement.ID
		activeChanged = true
	}
	if err := s.save(); err != nil {
		s.manifest = original
		return false, err
	}
	return activeChanged, nil
}

// RemoveProject removes EchoCode metadata for a project without touching its workspace.
func (s *Store) RemoveProject(projectID string) (removed Project, activeChanged bool, err error) {
	return s.removeProject(projectID, nil)
}

// RemoveProjectWithFallback replaces the final project with a fresh fallback workspace.
func (s *Store) RemoveProjectWithFallback(projectID, fallbackName, fallbackRoot string, extraRoots []string) (removed Project, activeChanged bool, err error) {
	return s.removeProject(projectID, &Project{
		Name:               strings.TrimSpace(fallbackName),
		WorkspaceRoot:      strings.TrimSpace(fallbackRoot),
		ExtraWritableRoots: append([]string(nil), extraRoots...),
	})
}

func (s *Store) removeProject(projectID string, fallback *Project) (removed Project, activeChanged bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	removeIndex := -1
	for i := range s.manifest.Projects {
		if s.manifest.Projects[i].ID == projectID {
			removeIndex = i
			break
		}
	}
	if removeIndex < 0 {
		return Project{}, false, fmt.Errorf("项目不存在: %s", projectID)
	}
	if len(s.manifest.Projects) == 1 {
		if fallback == nil {
			return Project{}, false, fmt.Errorf("至少需要保留一个项目")
		}
		if fallback.Name == "" || fallback.WorkspaceRoot == "" {
			return Project{}, false, fmt.Errorf("临时项目名称和目录不能为空")
		}
	}

	original := cloneManifest(s.manifest)
	s.rememberActiveSession()
	removed = cloneManifest(Manifest{Projects: []Project{s.manifest.Projects[removeIndex]}}).Projects[0]
	if !isGeneratedBootstrapProject(removed) {
		detachProject(&s.manifest.DetachedProjects, removed)
	}
	if len(s.manifest.Projects) == 1 {
		session := newSession("新对话")
		replacement := Project{
			ID:                  genID("proj"),
			Name:                fallback.Name,
			WorkspaceRoot:       fallback.WorkspaceRoot,
			ExtraWritableRoots:  append([]string(nil), fallback.ExtraWritableRoots...),
			CreatedAt:           nowRFC3339(),
			LastActiveSessionID: session.ID,
			Sessions:            []Session{session},
		}
		s.manifest.Projects = []Project{replacement}
		s.manifest.ActiveProjectID = replacement.ID
		s.manifest.ActiveSessionID = session.ID
		if err := s.save(); err != nil {
			s.manifest = original
			return Project{}, false, err
		}
		return removed, true, nil
	}

	activeChanged = removed.ID == s.manifest.ActiveProjectID
	s.manifest.Projects = append(s.manifest.Projects[:removeIndex], s.manifest.Projects[removeIndex+1:]...)
	if activeChanged {
		nextIndex := removeIndex
		if nextIndex >= len(s.manifest.Projects) {
			nextIndex = len(s.manifest.Projects) - 1
		}
		next := &s.manifest.Projects[nextIndex]
		s.manifest.ActiveProjectID = next.ID
		s.manifest.ActiveSessionID = ensureProjectActiveSession(next)
	}
	if err := s.save(); err != nil {
		s.manifest = original
		return Project{}, false, err
	}
	return removed, activeChanged, nil
}

func detachProject(detached *[]Project, project Project) {
	for index := range *detached {
		candidate := &(*detached)[index]
		if candidate.ID == project.ID || sameWorkspaceRoot(candidate.WorkspaceRoot, project.WorkspaceRoot) {
			*candidate = project
			return
		}
	}
	*detached = append(*detached, project)
}

// SwitchProject 切换活动项目，活动会话设为该项目最近的未归档会话（或新建）。
func (s *Store) SwitchProject(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := cloneManifest(s.manifest)
	p := s.findProject(projectID)
	if p == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	s.rememberActiveSession()
	s.manifest.ActiveProjectID = projectID
	s.manifest.ActiveSessionID = ensureProjectActiveSession(p)
	if err := s.save(); err != nil {
		s.manifest = original
		return err
	}
	return nil
}

func ensureProjectActiveSession(p *Project) string {
	if session := p.findSession(strings.TrimSpace(p.LastActiveSessionID)); session != nil && !session.Archived {
		return session.ID
	}
	pick := -1
	for i := range p.Sessions {
		if p.Sessions[i].Archived {
			continue
		}
		if pick == -1 || p.Sessions[i].UpdatedAt > p.Sessions[pick].UpdatedAt {
			pick = i
		}
	}
	if pick >= 0 {
		p.LastActiveSessionID = p.Sessions[pick].ID
		return p.LastActiveSessionID
	}
	replacement := newSession("新对话")
	p.Sessions = append(p.Sessions, replacement)
	p.LastActiveSessionID = replacement.ID
	return p.LastActiveSessionID
}

func (s *Store) rememberActiveSession() {
	project := s.findProject(s.manifest.ActiveProjectID)
	if project == nil || project.findSession(s.manifest.ActiveSessionID) == nil {
		return
	}
	project.LastActiveSessionID = s.manifest.ActiveSessionID
}

// NewSession 在活动项目下新建会话并切为活动。返回新会话。
func (s *Store) NewSession(title string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := cloneManifest(s.manifest)
	p := s.findProject(s.manifest.ActiveProjectID)
	if p == nil {
		return Session{}, fmt.Errorf("无活动项目")
	}
	if title == "" {
		title = "新对话"
	}
	sess := newSession(title)
	p.Sessions = append(p.Sessions, sess)
	s.manifest.ActiveSessionID = sess.ID
	p.LastActiveSessionID = sess.ID
	if err := s.save(); err != nil {
		s.manifest = original
		return Session{}, err
	}
	return sess, nil
}

// SwitchSession 切换活动会话（须属于活动项目）。
func (s *Store) SwitchSession(sessionID string) error {
	projectID, _ := s.ActiveIDs()
	return s.SwitchProjectSession(projectID, sessionID)
}

// SwitchProjectSession atomically switches both active pointers after
// validating the exact project/session pair.
func (s *Store) SwitchProjectSession(projectID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	p := s.findProject(projectID)
	if p == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	if p.findSession(sessionID) == nil {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	original := cloneManifest(s.manifest)
	s.rememberActiveSession()
	s.manifest.ActiveProjectID = projectID
	s.manifest.ActiveSessionID = sessionID
	p.LastActiveSessionID = sessionID
	if err := s.save(); err != nil {
		s.manifest = original
		return err
	}
	return nil
}

// SetSessionTitle 更新会话标题（并刷新 UpdatedAt）。
func (s *Store) SetSessionTitle(sessionID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(s.manifest.ActiveProjectID)
	if p == nil {
		return fmt.Errorf("无活动项目")
	}
	sess := p.findSession(sessionID)
	if sess == nil {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	if title != "" {
		sess.Title = title
	}
	sess.UpdatedAt = nowRFC3339()
	return s.save()
}

// SetSessionTitleForProject updates a session without relying on the mutable
// active-session pointer. Background conversations use this variant.
func (s *Store) SetSessionTitleForProject(projectID, sessionID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.findProject(strings.TrimSpace(projectID))
	if project == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	session := project.findSession(strings.TrimSpace(sessionID))
	if session == nil {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	if strings.TrimSpace(title) != "" {
		session.Title = title
	}
	session.UpdatedAt = nowRFC3339()
	return s.save()
}

// TouchActiveSession 刷新活动会话的更新时间（每轮对话后调用）。
func (s *Store) TouchActiveSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(s.manifest.ActiveProjectID)
	if p == nil {
		return nil
	}
	sess := p.findSession(s.manifest.ActiveSessionID)
	if sess == nil {
		return nil
	}
	sess.UpdatedAt = nowRFC3339()
	p.LastActiveSessionID = sess.ID
	return s.save()
}

// TouchSession updates a non-active session's timestamp without switching it.
func (s *Store) TouchSession(projectID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.findProject(strings.TrimSpace(projectID))
	if project == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	session := project.findSession(strings.TrimSpace(sessionID))
	if session == nil {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	session.UpdatedAt = nowRFC3339()
	return s.save()
}

// SetArchived 归档/取消归档会话。
func (s *Store) SetArchived(sessionID string, archived bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for pi := range s.manifest.Projects {
		if sess := s.manifest.Projects[pi].findSession(sessionID); sess != nil {
			sess.Archived = archived
			sess.UpdatedAt = nowRFC3339()
			return s.save()
		}
	}
	return fmt.Errorf("会话不存在: %s", sessionID)
}

// SetArchivedForProject updates an exact project/session pair.
func (s *Store) SetArchivedForProject(projectID, sessionID string, archived bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.findProject(strings.TrimSpace(projectID))
	if project == nil {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	session := project.findSession(strings.TrimSpace(sessionID))
	if session == nil {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	original := *session
	session.Archived = archived
	session.UpdatedAt = nowRFC3339()
	if err := s.save(); err != nil {
		*session = original
		return err
	}
	return nil
}

// DeleteSession 从清单中永久移除会话元数据。
// 删除活动会话时自动切换到最近的未归档会话；没有可用会话则创建一个空会话。
func (s *Store) DeleteSession(sessionID string) (projectID string, activeChanged bool, err error) {
	projectID, _, ok := s.FindSession(sessionID)
	if !ok {
		return "", false, fmt.Errorf("会话不存在或 ID 不唯一: %s", sessionID)
	}
	activeChanged, err = s.DeleteProjectSession(projectID, sessionID)
	return projectID, activeChanged, err
}

// DeleteProjectSession deletes only the exact project/session pair.
func (s *Store) DeleteProjectSession(projectID, sessionID string) (activeChanged bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := cloneManifest(s.manifest)
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	project := s.findProject(projectID)
	if project == nil {
		return false, fmt.Errorf("项目不存在: %s", projectID)
	}
	for sessionIndex := range project.Sessions {
		if project.Sessions[sessionIndex].ID != sessionID {
			continue
		}

		activeChanged = project.ID == s.manifest.ActiveProjectID && sessionID == s.manifest.ActiveSessionID
		project.Sessions = append(project.Sessions[:sessionIndex], project.Sessions[sessionIndex+1:]...)
		if project.LastActiveSessionID == sessionID {
			project.LastActiveSessionID = ""
		}

		if activeChanged {
			s.manifest.ActiveSessionID = ensureProjectActiveSession(project)
		}

		if saveErr := s.save(); saveErr != nil {
			s.manifest = original
			return false, saveErr
		}
		return activeChanged, nil
	}
	return false, fmt.Errorf("会话不存在: %s", sessionID)
}

// PruneGeneratedBootstrapProjects removes inactive empty projects that were
// accidentally created from a packaged application's build/bin directory.
// The strict signature avoids touching user-created projects with real chats.
func (s *Store) PruneGeneratedBootstrapProjects() ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.manifest.Projects) <= 1 {
		return nil, nil
	}
	original := cloneManifest(s.manifest)
	kept := make([]Project, 0, len(s.manifest.Projects))
	removed := make([]Project, 0, 1)
	for _, project := range s.manifest.Projects {
		if project.ID != s.manifest.ActiveProjectID && isGeneratedBootstrapProject(project) {
			removed = append(removed, project)
			continue
		}
		kept = append(kept, project)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	s.manifest.Projects = kept
	if err := s.save(); err != nil {
		s.manifest = original
		return nil, err
	}
	return removed, nil
}

func isGeneratedBootstrapProject(project Project) bool {
	root := filepath.Clean(strings.TrimSpace(project.WorkspaceRoot))
	if !strings.EqualFold(filepath.Base(root), "bin") || !strings.EqualFold(filepath.Base(filepath.Dir(root)), "build") {
		return false
	}
	if len(project.Sessions) != 1 {
		return false
	}
	session := project.Sessions[0]
	return !session.Archived && strings.TrimSpace(session.Title) == "新对话" && session.CreatedAt == session.UpdatedAt
}
