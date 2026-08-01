package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMemoryCarriesAcrossSessionsAndStaysFrozenWithinSession(t *testing.T) {
	svc := newProjectMemoryTestService(t)
	svc.recordUserEvent("Implement authentication middleware")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("Added authentication middleware and tests", "test-model", nil)

	if state := svc.WorkbenchState().ProjectMemory; state.SessionCount != 0 {
		t.Fatalf("active session must not enter its own memory: %#v", state)
	}
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	memory := svc.WorkbenchState().ProjectMemory
	if memory.SessionCount != 1 || memory.TurnCount != 1 {
		t.Fatalf("memory counts = %#v", memory)
	}
	if !strings.Contains(memory.Summary, "Implement authentication middleware") || !strings.Contains(memory.Summary, "Added authentication middleware") {
		t.Fatalf("memory summary = %q", memory.Summary)
	}
	frozenHash := memory.SnapshotHash

	svc.recordUserEvent("Improve integration coverage")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("Added integration coverage", "test-model", nil)
	if got := svc.WorkbenchState().ProjectMemory.SnapshotHash; got != frozenHash {
		t.Fatalf("memory hash changed inside active session: %s -> %s", frozenHash, got)
	}

	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	nextMemory := svc.WorkbenchState().ProjectMemory
	if nextMemory.SessionCount != 2 || !strings.Contains(nextMemory.Summary, "Improve integration coverage") {
		t.Fatalf("next session memory = %#v", nextMemory)
	}
	if nextMemory.SnapshotHash == frozenHash {
		t.Fatal("new session should refresh the frozen project memory snapshot")
	}
}

func TestProjectMemoryUsesCurrentRewindBranchOnly(t *testing.T) {
	svc := newProjectMemoryTestService(t)
	svc.recordUserEvent("Create baseline")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("Baseline complete", "test-model", nil)
	checkpoints := svc.ListCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("checkpoints = %d", len(checkpoints))
	}

	svc.recordUserEvent("Choose old implementation")
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("OLD_BRANCH_RESULT", "test-model", nil)
	if _, err := svc.RewindToCheckpoint(checkpoints[0].ID); err != nil {
		t.Fatal(err)
	}
	svc.recordUserEvent("Choose replacement implementation")
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("CURRENT_BRANCH_RESULT", "test-model", nil)

	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	summary := svc.WorkbenchState().ProjectMemory.Summary
	if !strings.Contains(summary, "CURRENT_BRANCH_RESULT") {
		t.Fatalf("current branch missing from memory: %q", summary)
	}
	if strings.Contains(summary, "OLD_BRANCH_RESULT") {
		t.Fatalf("abandoned branch leaked into memory: %q", summary)
	}
}

func TestProjectMemoryCanExcludeArchivedSessions(t *testing.T) {
	svc := newProjectMemoryTestService(t)
	sessions := svc.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	firstSessionID := sessions[0].ID
	svc.recordUserEvent("Archived request")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("ARCHIVED_RESULT", "test-model", nil)
	if _, err := svc.NewSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ArchiveSession(firstSessionID, true); err != nil {
		t.Fatal(err)
	}
	settings := svc.runtimeSettings
	settings.Memory.IncludeArchived = false
	if _, err := svc.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if summary := svc.WorkbenchState().ProjectMemory.Summary; strings.Contains(summary, "ARCHIVED_RESULT") {
		t.Fatalf("archived session should be excluded: %q", summary)
	}
}

func newProjectMemoryTestService(t *testing.T) *Service {
	t.Helper()
	base := t.TempDir()
	settingsPath := filepath.Join(base, "settings.json")
	settings := DefaultRuntimeSettings()
	settings.WorkspaceRoot = filepath.Join(base, "workspace")
	if err := saveRuntimeSettings(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	return NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SettingsPath: settingsPath,
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
}
