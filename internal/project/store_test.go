package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneGeneratedBootstrapProjectsRemovesInactiveBuildBin(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	buildBin := filepath.Join(t.TempDir(), "build", "bin")
	if _, err := store.EnsureBootstrap(buildBin, "bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject("real-project", t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	removed, err := store.PruneGeneratedBootstrapProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "bin" {
		t.Fatalf("removed projects = %#v", removed)
	}
	manifest := store.Snapshot()
	if len(manifest.Projects) != 1 || manifest.Projects[0].Name != "real-project" {
		t.Fatalf("remaining projects = %#v", manifest.Projects)
	}
}

func TestPruneGeneratedBootstrapProjectsKeepsActiveProject(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(filepath.Join(t.TempDir(), "build", "bin"), "bin"); err != nil {
		t.Fatal(err)
	}
	removed, err := store.PruneGeneratedBootstrapProjects()
	if err != nil || len(removed) != 0 || len(store.Snapshot().Projects) != 1 {
		t.Fatalf("active bootstrap project was pruned: removed=%#v err=%v", removed, err)
	}
}

func TestProjectPinRenameAndPersistence(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "projects.json")
	store, err := Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(t.TempDir(), "first"); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject("second", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetProjectPinned(second.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameProject(second.ID, "renamed"); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	project, ok := reopened.Project(second.ID)
	if !ok || !project.Pinned || project.Name != "renamed" {
		t.Fatalf("project after reopen = %#v, ok=%v", project, ok)
	}
}

func TestProjectSessionIdentityRejectsAmbiguousBareID(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(t.TempDir(), "first"); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject("second", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	firstID := store.manifest.Projects[0].ID
	sharedSessionID := "sess-shared"
	store.manifest.Projects[0].Sessions[0].ID = sharedSessionID
	store.manifest.Projects[1].Sessions[0].ID = sharedSessionID
	store.manifest.ActiveProjectID = second.ID
	store.manifest.ActiveSessionID = sharedSessionID
	err = store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if projectID, _, ok := store.FindSession(sharedSessionID); ok || projectID != "" {
		t.Fatalf("ambiguous bare session ID resolved to project %q", projectID)
	}
	if err := store.SwitchProjectSession(firstID, sharedSessionID); err != nil {
		t.Fatalf("switch exact project/session: %v", err)
	}
	if projectID, sessionID := store.ActiveIDs(); projectID != firstID || sessionID != sharedSessionID {
		t.Fatalf("active identity = %q/%q", projectID, sessionID)
	}
	if err := store.SetArchivedForProject(second.ID, sharedSessionID, true); err != nil {
		t.Fatal(err)
	}
	first, _ := store.ProjectSession(firstID, sharedSessionID)
	archivedSecond, _ := store.ProjectSession(second.ID, sharedSessionID)
	if first.Archived || !archivedSecond.Archived {
		t.Fatalf("archive crossed projects: first=%v second=%v", first.Archived, archivedSecond.Archived)
	}
	if err := store.SetSessionTitleForProject(second.ID, sharedSessionID, "second conversation"); err != nil {
		t.Fatal(err)
	}
	first, _ = store.ProjectSession(firstID, sharedSessionID)
	renamedSecond, _ := store.ProjectSession(second.ID, sharedSessionID)
	if first.Title == renamedSecond.Title || renamedSecond.Title != "second conversation" {
		t.Fatalf("rename crossed projects: first=%q second=%q", first.Title, renamedSecond.Title)
	}
	if _, _, err := store.DeleteSession(sharedSessionID); err == nil {
		t.Fatal("ambiguous bare session ID should not be deleted")
	}
	if _, err := store.DeleteProjectSession(second.ID, sharedSessionID); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ProjectSession(firstID, sharedSessionID); !ok {
		t.Fatal("exact delete removed the other project's session")
	}
}

func TestArchiveProjectSessionsCreatesActiveReplacement(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(t.TempDir(), "project"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewSession("second task"); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	projectID := before.ActiveProjectID
	oldSessionIDs := map[string]bool{}
	for _, session := range before.Projects[0].Sessions {
		oldSessionIDs[session.ID] = true
	}

	activeChanged, err := store.SetProjectSessionsArchived(projectID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !activeChanged {
		t.Fatal("archiving the active project should create a replacement task")
	}
	after := store.Snapshot()
	if oldSessionIDs[after.ActiveSessionID] {
		t.Fatalf("active session was not replaced: %q", after.ActiveSessionID)
	}
	project := after.Projects[0]
	if len(project.Sessions) != len(oldSessionIDs)+1 {
		t.Fatalf("sessions = %d, want %d", len(project.Sessions), len(oldSessionIDs)+1)
	}
	for _, session := range project.Sessions {
		if oldSessionIDs[session.ID] && !session.Archived {
			t.Fatalf("old session was not archived: %#v", session)
		}
		if session.ID == after.ActiveSessionID && session.Archived {
			t.Fatalf("replacement session is archived: %#v", session)
		}
	}
}

func TestRemoveProjectSwitchesActiveAndKeepsWorkspace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := t.TempDir()
	if _, err := store.EnsureBootstrap(firstRoot, "first"); err != nil {
		t.Fatal(err)
	}
	secondRoot := t.TempDir()
	marker := filepath.Join(secondRoot, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject("second", secondRoot, nil)
	if err != nil {
		t.Fatal(err)
	}

	removed, activeChanged, err := store.RemoveProject(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !activeChanged || removed.WorkspaceRoot != secondRoot {
		t.Fatalf("removed=%#v activeChanged=%v", removed, activeChanged)
	}
	manifest := store.Snapshot()
	if len(manifest.Projects) != 1 || manifest.ActiveProjectID != manifest.Projects[0].ID || manifest.Projects[0].WorkspaceRoot != firstRoot {
		t.Fatalf("manifest after removal = %#v", manifest)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("workspace content was touched: %v", err)
	}
	fallbackRoot := t.TempDir()
	removed, activeChanged, err = store.RemoveProjectWithFallback(manifest.ActiveProjectID, "EchoCodeProject", fallbackRoot, nil)
	if err != nil || !activeChanged || removed.ID == "" {
		t.Fatalf("fallback removal = removed=%#v activeChanged=%v err=%v", removed, activeChanged, err)
	}
	manifest = store.Snapshot()
	if len(manifest.Projects) != 1 || manifest.Projects[0].Name != "EchoCodeProject" || manifest.Projects[0].WorkspaceRoot != fallbackRoot {
		t.Fatalf("fallback project = %#v", manifest.Projects)
	}
}

func TestReattachProjectAfterRestartRestoresLastActiveSession(t *testing.T) {
	base := t.TempDir()
	manifestPath := filepath.Join(base, "projects.json")
	workspace := filepath.Join(base, "workspace")
	fallback := filepath.Join(base, "fallback")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(workspace, "workspace"); err != nil {
		t.Fatal(err)
	}
	projectID, firstSessionID := store.ActiveIDs()
	selected, err := store.NewSession("selected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewSession("newer but not selected"); err != nil {
		t.Fatal(err)
	}
	if err := store.SwitchProjectSession(projectID, selected.ID); err != nil {
		t.Fatal(err)
	}
	if selected.ID == firstSessionID {
		t.Fatal("test setup did not create a distinct selected session")
	}
	if _, _, err := store.RemoveProjectWithFallback(projectID, "fallback", fallback, nil); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.CreateProject("workspace", workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	restoredProjectID, restoredSessionID := restarted.ActiveIDs()
	if restored.ID != projectID || restoredProjectID != projectID {
		t.Fatalf("restored project identity = %q/%q, want %q", restored.ID, restoredProjectID, projectID)
	}
	if restoredSessionID != selected.ID {
		t.Fatalf("restored session = %q, want last active %q", restoredSessionID, selected.ID)
	}
}
