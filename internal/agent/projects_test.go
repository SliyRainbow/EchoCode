package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// TestMultiSessionIsolation 验证不同会话的事件日志相互隔离。
func TestMultiSessionIsolation(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	if svc.projects == nil || svc.eventStore == nil {
		t.Fatal("项目清单与事件存储应已初始化")
	}

	// 默认项目 + 默认会话，记一条事件。
	svc.sessionMessages = nil
	svc.recordUserEvent("会话A的消息")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("A回复", "m", nil)
	if len(svc.ListCheckpoints()) != 1 {
		t.Fatalf("会话A应有1个检查点")
	}

	// 新建会话B → 事件日志应是空的。
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	if len(svc.ListCheckpoints()) != 0 {
		t.Fatalf("新会话B应无检查点, got %d", len(svc.ListCheckpoints()))
	}

	svc.recordUserEvent("会话B的消息")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("B回复", "m", nil)

	// 应有两个会话。
	sessions := svc.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("应有2个会话, got %d", len(sessions))
	}

	// 切回会话A → 它的检查点应还在（隔离验证）。
	var sessionAID string
	for _, s := range sessions {
		if !s.IsActive {
			sessionAID = s.ID
		}
	}
	if _, err := svc.SwitchSession(sessionAID); err != nil {
		t.Fatal(err)
	}
	if len(svc.ListCheckpoints()) != 1 {
		t.Fatalf("切回会话A应恢复其1个检查点, got %d", len(svc.ListCheckpoints()))
	}
}

// TestProjectSwitchChangesWorkspace 验证切换项目会切换工作区根。
func TestProjectSwitchChangesWorkspace(t *testing.T) {
	base := t.TempDir()
	ws1 := t.TempDir()
	ws2 := t.TempDir()

	svc := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	svc.runtimeSettings.WorkspaceRoot = ws1

	projects := svc.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("应有1个默认项目, got %d", len(projects))
	}

	// 新建指向 ws2 的项目并切换。
	if _, err := svc.CreateProject("项目2", ws2); err != nil {
		t.Fatal(err)
	}
	if svc.runtimeSettings.WorkspaceRoot != ws2 {
		t.Fatalf("切到项目2后工作区应为 ws2, got %q", svc.runtimeSettings.WorkspaceRoot)
	}
	if len(svc.ListProjects()) != 2 {
		t.Fatalf("应有2个项目")
	}
}

// TestArchiveSession 验证归档标记。
func TestArchiveSession(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	sessions := svc.ListSessions()
	id := sessions[0].ID
	if _, err := svc.ArchiveSession(id, true); err != nil {
		t.Fatal(err)
	}
	for _, s := range svc.ListSessions() {
		if s.ID == id && !s.Archived {
			t.Fatal("会话应已归档")
		}
	}
}

func TestRenameProjectSession(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	projectID, sessionID := svc.ActiveSessionIDs()
	state, err := svc.RenameProjectSession(projectID, sessionID, "  renamed conversation  ")
	if err != nil {
		t.Fatal(err)
	}
	sessions := svc.ListSessions()
	if state.ActiveSessionID != sessionID || len(sessions) != 1 || sessions[0].Title != "renamed conversation" {
		t.Fatalf("renamed sessions = %#v", sessions)
	}
	if _, err := svc.RenameProjectSession(projectID, sessionID, "  "); err == nil {
		t.Fatal("empty title should be rejected")
	}
}

// TestPersistenceAcrossReopen 验证项目清单重开后仍在。
func TestPersistenceAcrossReopen(t *testing.T) {
	base := t.TempDir()
	cfg := ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	}
	svc := NewService(cfg)
	_, _ = svc.CreateProject("持久项目", t.TempDir())
	before := len(svc.ListProjects())

	// 重新构造 Service（模拟重启）。
	svc2 := NewService(cfg)
	if len(svc2.ListProjects()) != before {
		t.Fatalf("重启后项目数应为 %d, got %d", before, len(svc2.ListProjects()))
	}
}

func TestDeleteInactiveSessionRemovesEventDirectory(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "sessions")
	svc := NewService(ServiceConfig{
		SkillsDir: t.TempDir(), SessionsDir: sessionsDir, ProjectsPath: filepath.Join(base, "projects.json"), TemporaryWorkspaceRoot: filepath.Join(base, "EchoCodeProject"),
	})
	projectID, firstID := svc.projects.ActiveIDs()
	svc.recordUserEvent("to be deleted")
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	_, activeID := svc.projects.ActiveIDs()
	if _, err := svc.DeleteSession(firstID); err != nil {
		t.Fatal(err)
	}
	if _, currentID := svc.projects.ActiveIDs(); currentID != activeID {
		t.Fatalf("删除非活动会话后 active = %q, want %q", currentID, activeID)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, projectID, firstID)); !os.IsNotExist(err) {
		t.Fatalf("旧事件目录仍存在或检查失败: %v", err)
	}
}

func TestDeleteActiveSessionSelectsExistingSession(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceConfig{
		SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions"), ProjectsPath: filepath.Join(base, "projects.json"),
	})
	_, firstID := svc.projects.ActiveIDs()
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	_, secondID := svc.projects.ActiveIDs()
	if _, err := svc.DeleteSession(secondID); err != nil {
		t.Fatal(err)
	}
	if _, activeID := svc.projects.ActiveIDs(); activeID != firstID {
		t.Fatalf("删除活动会话后 active = %q, want %q", activeID, firstID)
	}
	if got := len(svc.ListSessions()); got != 1 {
		t.Fatalf("剩余会话数 = %d, want 1", got)
	}
}

func TestDeleteLastSessionCreatesEmptyReplacement(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "sessions")
	svc := NewService(ServiceConfig{
		SkillsDir: t.TempDir(), SessionsDir: sessionsDir, ProjectsPath: filepath.Join(base, "projects.json"),
	})
	projectID, oldID := svc.projects.ActiveIDs()
	svc.recordUserEvent("old conversation")
	if _, err := svc.DeleteSession(oldID); err != nil {
		t.Fatal(err)
	}
	_, replacementID := svc.projects.ActiveIDs()
	if replacementID == "" || replacementID == oldID {
		t.Fatalf("替代会话 ID = %q", replacementID)
	}
	if history := svc.GetSessionMessages(); len(history) != 0 {
		t.Fatalf("替代会话应为空: %#v", history)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, projectID, oldID)); !os.IsNotExist(err) {
		t.Fatalf("旧事件目录仍存在或检查失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, projectID, replacementID)); err != nil {
		t.Fatalf("替代会话目录未创建: %v", err)
	}
}

func TestProjectPinRenameAndSortPersist(t *testing.T) {
	base := t.TempDir()
	cfg := ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	}
	svc := NewService(cfg)
	state, err := svc.CreateProject("second", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = state
	projects := svc.ListProjects()
	secondID := projects[len(projects)-1].ID
	if _, err := svc.SetProjectPinned(secondID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenameProject(secondID, "pinned project"); err != nil {
		t.Fatal(err)
	}
	projects = svc.ListProjects()
	if len(projects) < 2 || projects[0].ID != secondID || !projects[0].Pinned || projects[0].Name != "pinned project" {
		t.Fatalf("sorted projects = %#v", projects)
	}

	reopened := NewService(cfg)
	projects = reopened.ListProjects()
	if len(projects) < 2 || projects[0].ID != secondID || !projects[0].Pinned || projects[0].Name != "pinned project" {
		t.Fatalf("projects after reopen = %#v", projects)
	}
}

func TestArchiveProjectTasksKeepsFreshActiveSession(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceConfig{
		SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions"), ProjectsPath: filepath.Join(base, "projects.json"),
	})
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	projectID, activeBefore := svc.projects.ActiveIDs()
	if _, err := svc.ArchiveProjectTasks(projectID); err != nil {
		t.Fatal(err)
	}
	_, activeAfter := svc.projects.ActiveIDs()
	if activeAfter == "" || activeAfter == activeBefore {
		t.Fatalf("active session = %q, previous = %q", activeAfter, activeBefore)
	}
	sessions := svc.ListSessions()
	activeCount := 0
	archivedCount := 0
	for _, session := range sessions {
		if session.IsActive {
			activeCount++
			if session.Archived {
				t.Fatalf("active replacement is archived: %#v", session)
			}
		} else if session.Archived {
			archivedCount++
		}
	}
	if activeCount != 1 || archivedCount != len(sessions)-1 {
		t.Fatalf("sessions after archive = %#v", sessions)
	}
	if history := svc.GetSessionMessages(); len(history) != 0 {
		t.Fatalf("replacement task should be empty: %#v", history)
	}
}

func TestRemoveAndReattachProjectPreservesSessionDataAndWorkspace(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "sessions")
	svc := NewService(ServiceConfig{
		SkillsDir:              t.TempDir(),
		SessionsDir:            sessionsDir,
		ProjectsPath:           filepath.Join(base, "projects.json"),
		TemporaryWorkspaceRoot: filepath.Join(base, "EchoCodeProject"),
	})
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "source.txt")
	if err := os.WriteFile(marker, []byte("keep source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject("remove me", workspace); err != nil {
		t.Fatal(err)
	}
	projectID, sessionID := svc.projects.ActiveIDs()
	svc.recordUserEvent("persisted task")
	svc.sessionState.TurnCount++
	svc.recordAssistantAndCheckpoint("persisted reply", "test-model", nil)
	projectEventDir := filepath.Join(sessionsDir, projectID)
	if _, err := os.Stat(projectEventDir); err != nil {
		t.Fatalf("project event directory was not created: %v", err)
	}

	if _, err := svc.RemoveProject(projectID); err != nil {
		t.Fatal(err)
	}
	for _, project := range svc.ListProjects() {
		if project.ID == projectID {
			t.Fatalf("removed project is still listed: %#v", project)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("workspace source was removed: %v", err)
	}
	if _, err := os.Stat(projectEventDir); err != nil {
		t.Fatalf("project event directory was removed: %v", err)
	}
	if svc.runtimeSettings.WorkspaceRoot == workspace {
		t.Fatalf("active workspace still points at removed project: %q", workspace)
	}
	remaining := svc.ListProjects()
	if len(remaining) != 1 {
		t.Fatalf("remaining projects = %#v", remaining)
	}
	if _, err := svc.CreateProject("remove me", workspace); err != nil {
		t.Fatal(err)
	}
	restoredProjectID, restoredSessionID := svc.projects.ActiveIDs()
	if restoredProjectID != projectID || restoredSessionID != sessionID {
		t.Fatalf("reattached identity = %s/%s, want %s/%s", restoredProjectID, restoredSessionID, projectID, sessionID)
	}
	history := svc.GetSessionMessages()
	if len(history) != 2 || history[0].Content != "persisted task" || history[1].Content != "persisted reply" {
		t.Fatalf("reattached history = %#v", history)
	}
	if _, err := svc.RemoveProject(restoredProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProject(remaining[0].ID); err != nil {
		t.Fatal(err)
	}
	final := svc.ListProjects()
	if len(final) != 1 || final[0].Name != "EchoCodeProject" || final[0].WorkspaceRoot != filepath.Join(base, "EchoCodeProject") {
		t.Fatalf("fallback project = %#v", final)
	}
	if svc.runtimeSettings.WorkspaceRoot != filepath.Join(base, "EchoCodeProject") {
		t.Fatalf("runtime workspace = %q", svc.runtimeSettings.WorkspaceRoot)
	}
}

func TestRemoveRestartAndReattachRestoresConversationRuntime(t *testing.T) {
	base := t.TempDir()
	settingsPath := filepath.Join(base, "settings.json")
	defaultWorkspace := filepath.Join(base, "default-workspace")
	workspace := filepath.Join(base, "reattach-workspace")
	if err := os.MkdirAll(defaultWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := DefaultRuntimeSettings()
	settings.WorkspaceRoot = defaultWorkspace
	settings.Team.Enabled = true
	if err := saveRuntimeSettings(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	config := ServiceConfig{
		SkillsDir:              t.TempDir(),
		SettingsPath:           settingsPath,
		SessionsDir:            filepath.Join(base, "sessions"),
		ProjectsPath:           filepath.Join(base, "projects.json"),
		TemporaryWorkspaceRoot: filepath.Join(base, "EchoCodeProject"),
	}
	svc := NewService(config)
	if _, err := svc.CreateProject("reattach", workspace); err != nil {
		t.Fatal(err)
	}
	projectID, memorySessionID := svc.projects.ActiveIDs()
	svc.recordUserEvent("remember the durable project decision")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("DURABLE_PROJECT_MEMORY", "memory-model", nil)

	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	_, branchSessionID := svc.projects.ActiveIDs()
	svc.recordUserEvent("create branch baseline")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("BRANCH_BASELINE", "branch-model", nil)
	baselineCheckpoint := svc.ListCheckpoints()[0].ID
	svc.recordUserEvent("keep old branch")
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("OLD_BRANCH_ONLY", "branch-model", nil)
	oldBranchLeaf := svc.eventStore.Head()
	if _, err := svc.RewindToCheckpoint(baselineCheckpoint); err != nil {
		t.Fatal(err)
	}
	svc.recordUserEvent("use replacement branch")
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("CURRENT_BRANCH_ONLY", "branch-model", nil)
	if err := svc.startPlanState([]tools.ProgressStep{
		{Title: "preserve runtime", Status: "completed"},
		{Title: "resume review", Status: "in_progress"},
	}); err != nil {
		t.Fatal(err)
	}
	svc.teamState = TeamState{
		Enabled: true, Active: false, RunID: "team-remount", Status: "paused", CurrentRole: TeamRoleReviewer,
		Roles: []TeamRoleState{{Role: TeamRoleReviewer, Label: "审阅", Enabled: true, Status: "paused", Attempt: 2}},
	}
	teamCheckpoint := newTeamRunCheckpoint(settings.Team)
	teamCheckpoint.Status = "paused"
	teamCheckpoint.NextRole = TeamRoleReviewer
	teamCheckpoint.NextAttempt = 2
	teamCheckpoint.ReviewRound = 1
	teamCheckpoint.Artifacts = []teamCheckpointArtifact{{Role: TeamRoleImplementer, Attempt: 1, Content: "implementation retained"}}
	if err := svc.persistTeamRunCheckpoint(teamCheckpoint); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	_, newerSessionID := svc.projects.ActiveIDs()
	svc.recordUserEvent("newer session that must not steal activation")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("NEWER_SESSION", "other-model", nil)
	if _, err := svc.SwitchProjectSession(projectID, branchSessionID); err != nil {
		t.Fatal(err)
	}
	if memorySessionID == branchSessionID || branchSessionID == newerSessionID {
		t.Fatalf("test sessions are not distinct: %q %q %q", memorySessionID, branchSessionID, newerSessionID)
	}

	headBefore := svc.eventStore.Head()
	branchesBefore := svc.ListBranches()
	checkpointsBefore := svc.ListCheckpoints()
	historyBefore := svc.GetSessionMessages()
	memoryBefore := svc.WorkbenchState().ProjectMemory
	prefixBefore := svc.contextPreviewForInput("继续").PrefixHash
	providerSessionBefore := svc.providerSessionID()
	if len(branchesBefore) != 2 || len(checkpointsBefore) != 2 {
		t.Fatalf("branch setup = branches %#v checkpoints %#v", branchesBefore, checkpointsBefore)
	}
	if !strings.Contains(memoryBefore.Summary, "DURABLE_PROJECT_MEMORY") || !strings.Contains(memoryBefore.Summary, "NEWER_SESSION") {
		t.Fatalf("project memory before detach = %#v", memoryBefore)
	}
	if svc.teamResume == nil || svc.teamResume.NextRole != TeamRoleReviewer || svc.planState.Status != "running" {
		t.Fatalf("runtime before detach: team=%#v plan=%#v", svc.teamResume, svc.planState)
	}

	if _, err := svc.RemoveProject(projectID); err != nil {
		t.Fatal(err)
	}
	svc = NewService(config)
	for _, project := range svc.ListProjects() {
		if project.ID == projectID {
			t.Fatalf("detached project returned after restart: %#v", project)
		}
	}
	if _, err := svc.CreateProject("reattach", workspace); err != nil {
		t.Fatal(err)
	}
	restoredProjectID, restoredSessionID := svc.projects.ActiveIDs()
	if restoredProjectID != projectID || restoredSessionID != branchSessionID {
		t.Fatalf("reattached identity = %s/%s, want %s/%s", restoredProjectID, restoredSessionID, projectID, branchSessionID)
	}
	if svc.eventStore.Head() != headBefore {
		t.Fatalf("restored head = %q, want %q", svc.eventStore.Head(), headBefore)
	}
	if providerSession := svc.providerSessionID(); providerSession != providerSessionBefore {
		t.Fatalf("provider session identity = %q, want %q", providerSession, providerSessionBefore)
	}
	if prefix := svc.contextPreviewForInput("继续").PrefixHash; prefix != prefixBefore {
		t.Fatalf("stable prefix hash = %q, want %q", prefix, prefixBefore)
	}
	if got := svc.GetSessionMessages(); !sameSessionMessageContents(got, historyBefore) {
		t.Fatalf("restored history = %#v, want %#v", got, historyBefore)
	}
	if got := svc.ListCheckpoints(); !sameCheckpointIDs(got, checkpointsBefore) {
		t.Fatalf("restored checkpoints = %#v, want %#v", got, checkpointsBefore)
	}
	if got := svc.ListBranches(); !sameBranchIdentity(got, branchesBefore) {
		t.Fatalf("restored branches = %#v, want %#v", got, branchesBefore)
	}
	if !containsBranchLeaf(svc.ListBranches(), oldBranchLeaf) {
		t.Fatalf("old branch leaf %q was lost: %#v", oldBranchLeaf, svc.ListBranches())
	}
	if svc.teamResume == nil || svc.teamResume.NextRole != TeamRoleReviewer || svc.teamResume.NextAttempt != 2 || svc.teamState.Status != "paused" {
		t.Fatalf("restored team runtime = checkpoint %#v state %#v", svc.teamResume, svc.teamState)
	}
	if svc.planState.Status != "running" || len(svc.planState.Steps) != 2 || svc.planState.Steps[1].Status != "in_progress" {
		t.Fatalf("restored plan = %#v", svc.planState)
	}
	if memory := svc.WorkbenchState().ProjectMemory; memory.SnapshotHash != memoryBefore.SnapshotHash || memory.Summary != memoryBefore.Summary {
		t.Fatalf("restored project memory = %#v, want %#v", memory, memoryBefore)
	}
}

func sameSessionMessageContents(left, right []SessionMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Role != right[index].Role || left[index].Content != right[index].Content || left[index].Status != right[index].Status {
			return false
		}
	}
	return true
}

func sameCheckpointIDs(left, right []CheckpointInfo) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID {
			return false
		}
	}
	return true
}

func sameBranchIdentity(left, right []BranchInfo) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].LeafID != right[index].LeafID || left[index].IsCurrent != right[index].IsCurrent || left[index].TurnCount != right[index].TurnCount {
			return false
		}
	}
	return true
}

func containsBranchLeaf(branches []BranchInfo, leafID string) bool {
	for _, branch := range branches {
		if branch.LeafID == leafID {
			return true
		}
	}
	return false
}

func TestOpenProjectInFileManagerUsesConfiguredWorkspace(t *testing.T) {
	base := t.TempDir()
	workspace := t.TempDir()
	opened := ""
	svc := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
		OpenFile: func(path string) error {
			opened = path
			return nil
		},
	})
	if _, err := svc.CreateProject("open me", workspace); err != nil {
		t.Fatal(err)
	}
	projectID, _ := svc.projects.ActiveIDs()
	if err := svc.OpenProjectInFileManager(projectID); err != nil {
		t.Fatal(err)
	}
	if opened != workspace {
		t.Fatalf("opened = %q, want %q", opened, workspace)
	}
}

var _ = tools.FileChange{} // 保持 tools 依赖（未来扩展用）
