package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestSwitchBranchPreservesExternalChangesOnReplayOnlyPaths(t *testing.T) {
	for _, existed := range []bool{true, false} {
		t.Run(map[bool]string{true: "modified", false: "created"}[existed], func(t *testing.T) {
			workspace := t.TempDir()
			service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
			defer service.Close()
			service.runtimeSettings.WorkspaceRoot = workspace
			service.runtimeSettings.FilesystemAccess = "workspace-write"
			target := filepath.Join(workspace, "target-only.txt")
			if existed {
				if err := os.WriteFile(target, []byte("baseline"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.recordAssistantAndCheckpoint("baseline", "test", nil); err != nil {
				t.Fatal(err)
			}
			baseline := service.eventStore.Head()
			if err := service.recordFileSnapshot(tools.FileChange{Path: "target-only.txt", Before: "baseline", After: "branch content", Existed: existed, LineEnding: "lf"}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("branch content"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := service.recordAssistantAndCheckpoint("target branch", "test", nil); err != nil {
				t.Fatal(err)
			}
			tip := service.eventStore.Head()
			if _, err := service.RewindToCheckpoint(baseline); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("external edit"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SwitchBranch(tip); err == nil {
				t.Fatal("target replay overwrote an external edit")
			}
			if got, _ := os.ReadFile(target); string(got) != "external edit" {
				t.Fatalf("external content lost: %q", got)
			}
			if service.eventStore.Head() != baseline {
				t.Fatal("failed replay moved HEAD")
			}
		})
	}
}

func TestRewindCanReturnToOriginalTip(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "timeline.txt")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	metadata := tools.FileText{LineEnding: tools.LineEndingLF}
	if err := tools.WriteFileTextAtomic(target, "first\n", metadata); err != nil {
		t.Fatal(err)
	}
	service.recordUserEvent("first turn")
	service.sessionState.TurnCount = 1
	service.recordAssistantAndCheckpoint("first reply", "test-model", nil)
	first := service.ListCheckpoints()[0].ID
	service.recordUserEvent("second turn")
	if err := service.recordFileSnapshot(tools.FileChange{Path: "timeline.txt", Before: "first\n", After: "second\n", Existed: true, LineEnding: "lf"}); err != nil {
		t.Fatal(err)
	}
	if err := tools.WriteFileTextAtomic(target, "second\n", metadata); err != nil {
		t.Fatal(err)
	}
	service.sessionState.TurnCount = 2
	service.recordAssistantAndCheckpoint("second reply", "test-model", nil)
	tip := service.eventStore.Head()
	if _, err := service.RewindToCheckpoint(first); err != nil {
		t.Fatal(err)
	}
	checkpoints := service.ListCheckpoints()
	if len(checkpoints) != 1 || checkpoints[0].ID != first || !checkpoints[0].IsCurrent {
		t.Fatalf("current checkpoint is not identified: %#v", checkpoints)
	}
	branches := service.ListBranches()
	if len(branches) != 1 || branches[0].LeafID != tip || branches[0].IsCurrent {
		t.Fatalf("the original tip must remain selectable after rewind: %#v", branches)
	}
	if file, err := tools.ReadFileText(target); err != nil || file.Content != "first\n" {
		t.Fatalf("rewind did not restore the first file: %#v, %v", file, err)
	}
	if _, err := service.SwitchBranch(tip); err != nil {
		t.Fatal(err)
	}
	if file, err := tools.ReadFileText(target); err != nil || file.Content != "second\n" {
		t.Fatalf("returning to the tip did not replay the file: %#v, %v", file, err)
	}
	if history := service.GetSessionMessages(); len(history) != 4 || history[3].Content != "second reply" {
		t.Fatalf("returning to the tip did not restore conversation history: %#v", history)
	}
	if branches = service.ListBranches(); len(branches) != 1 || !branches[0].IsCurrent {
		t.Fatalf("the restored tip should now be current: %#v", branches)
	}
}

// TestSwitchBranchMovesFilesAcrossLines 验证分支切换时文件正确在两条线间切换。
func TestSwitchBranchMovesFilesAcrossLines(t *testing.T) {
	workspace := t.TempDir()
	sessions := t.TempDir()
	target := filepath.Join(workspace, "note.txt")

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.sessionMessages = nil

	// 基线 v1。
	if err := tools.WriteFileTextAtomic(target, "v1\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}

	// 第 1 轮：v1 → v2。
	svc.recordUserEvent("改成 v2")
	svc.recordFileSnapshot(tools.FileChange{Path: "note.txt", Before: "v1\n", After: "v2\n", Existed: true, LineEnding: "lf"})
	_ = tools.WriteFileTextAtomic(target, "v2\n", tools.FileText{LineEnding: tools.LineEndingLF})
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("已改 v2", "m", nil)
	cp1 := svc.ListCheckpoints()[0].ID

	// 第 2 轮（分支 A）：v2 → v3a。
	svc.recordUserEvent("改成 v3a")
	svc.recordFileSnapshot(tools.FileChange{Path: "note.txt", Before: "v2\n", After: "v3a\n", Existed: true, LineEnding: "lf"})
	_ = tools.WriteFileTextAtomic(target, "v3a\n", tools.FileText{LineEnding: tools.LineEndingLF})
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("已改 v3a", "m", nil)
	branchALeaf := svc.eventStore.Head()

	// 回退到 cp1（文件回到 v2），再分叉出分支 B：v2 → v3b。
	if _, err := svc.RewindToCheckpoint(cp1); err != nil {
		t.Fatal(err)
	}
	if got, _ := tools.ReadFileText(target); got.Content != "v2\n" {
		t.Fatalf("回退后应为 v2, got %q", got.Content)
	}
	svc.recordUserEvent("改成 v3b")
	svc.recordFileSnapshot(tools.FileChange{Path: "note.txt", Before: "v2\n", After: "v3b\n", Existed: true, LineEnding: "lf"})
	_ = tools.WriteFileTextAtomic(target, "v3b\n", tools.FileText{LineEnding: tools.LineEndingLF})
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("已改 v3b", "m", nil)

	// 现在应有两条分支。
	branches := svc.ListBranches()
	if len(branches) != 2 {
		t.Fatalf("分支数 = %d, want 2", len(branches))
	}

	// 当前在分支 B（v3b）。切换到分支 A → 文件应变回 v3a。
	if got, _ := tools.ReadFileText(target); got.Content != "v3b\n" {
		t.Fatalf("当前应为 v3b, got %q", got.Content)
	}
	if _, err := svc.SwitchBranch(branchALeaf); err != nil {
		t.Fatal(err)
	}
	if got, _ := tools.ReadFileText(target); got.Content != "v3a\n" {
		t.Fatalf("切换到分支 A 后应为 v3a, got %q", got.Content)
	}
}

func TestForkFromUserMessageRestoresPriorTurn(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "note.txt")
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	if err := tools.WriteFileTextAtomic(target, "v1\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}

	svc.recordUserEvent("first")
	if err := svc.recordFileSnapshot(tools.FileChange{Path: "note.txt", Before: "v1\n", After: "v2\n", Existed: true, LineEnding: "lf"}); err != nil {
		t.Fatal(err)
	}
	_ = tools.WriteFileTextAtomic(target, "v2\n", tools.FileText{LineEnding: tools.LineEndingLF})
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("first reply", "m", nil)

	svc.recordUserEvent("second")
	if err := svc.recordFileSnapshot(tools.FileChange{Path: "note.txt", Before: "v2\n", After: "v3\n", Existed: true, LineEnding: "lf"}); err != nil {
		t.Fatal(err)
	}
	_ = tools.WriteFileTextAtomic(target, "v3\n", tools.FileText{LineEnding: tools.LineEndingLF})
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("second reply", "m", nil)

	history := svc.GetSessionMessages()
	if len(history) != 4 || history[2].ID == "" {
		t.Fatalf("历史消息事件 ID 未正确暴露: %#v", history)
	}
	if _, err := svc.ForkFromMessage(history[2].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := tools.ReadFileText(target); got.Content != "v2\n" {
		t.Fatalf("从第二条用户消息分叉后文件 = %q, want v2", got.Content)
	}
	forkedHistory := svc.GetSessionMessages()
	if len(forkedHistory) != 2 || forkedHistory[0].Content != "first" || forkedHistory[1].Content != "first reply" {
		t.Fatalf("分叉后历史不正确: %#v", forkedHistory)
	}
	head, ok := svc.eventStore.Event(svc.eventStore.Head())
	if !ok || head.Type != eventlog.EventBranchMarker {
		t.Fatalf("分叉后 head 应为 branch marker, got %#v", head)
	}
	if branches := svc.ListBranches(); len(branches) != 2 {
		t.Fatalf("分叉数 = %d, want 2", len(branches))
	}
}

func TestForkFromAssistantKeepsReplyCheckpoint(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.recordUserEvent("first")
	svc.sessionState.TurnCount = 1
	svc.recordAssistantAndCheckpoint("first reply", "m", nil)
	svc.recordUserEvent("second")
	svc.sessionState.TurnCount = 2
	svc.recordAssistantAndCheckpoint("second reply", "m", nil)

	history := svc.GetSessionMessages()
	if _, err := svc.ForkFromMessage(history[1].ID); err != nil {
		t.Fatal(err)
	}
	forkedHistory := svc.GetSessionMessages()
	if len(forkedHistory) != 2 || forkedHistory[1].Content != "first reply" {
		t.Fatalf("助手消息分叉应保留该回复: %#v", forkedHistory)
	}
	if svc.sessionState.TurnCount != 1 {
		t.Fatalf("分叉后轮数 = %d, want 1", svc.sessionState.TurnCount)
	}
}

func TestForkFromUserMessageRestoresFilesAndClearsPausedTeamCheckpoint(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "team-edit.txt")
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	// This test targets event-branch semantics, not path canonicalization.
	svc.runtimeSettings.FilesystemAccess = "unrestricted"
	if err := tools.WriteFileTextAtomic(target, "before\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}

	svc.recordUserEvent("original team request")
	if err := svc.recordFileSnapshot(tools.FileChange{Path: "team-edit.txt", Before: "before\n", After: "after\n", Existed: true, LineEnding: "lf"}); err != nil {
		t.Fatal(err)
	}
	if err := tools.WriteFileTextAtomic(target, "after\n", tools.FileText{LineEnding: tools.LineEndingLF}); err != nil {
		t.Fatal(err)
	}
	svc.teamState = TeamState{
		Enabled: true, Status: "paused", CurrentRole: TeamRoleReviewer,
		Roles: []TeamRoleState{{Role: TeamRoleReviewer, Label: "审阅", Enabled: true, Status: "paused", Attempt: 1}},
	}
	checkpoint := newTeamRunCheckpoint(TeamSettings{})
	checkpoint.Status = "paused"
	checkpoint.NextRole = TeamRoleReviewer
	if err := svc.persistTeamRunCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	svc.recordTeamPauseMessage("AI 团队已暂停，请使用“继续任务”操作恢复。", "model-reviewer", nil)

	history := svc.GetSessionMessages()
	if len(history) != 2 || history[0].Role != "user" {
		t.Fatalf("paused history = %#v", history)
	}
	if _, err := svc.ForkFromMessage(history[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := tools.ReadFileText(target); got.Content != "before\n" {
		t.Fatalf("edited resend did not restore files: %q", got.Content)
	}
	if svc.teamResume != nil || svc.teamState.Status != "idle" {
		t.Fatalf("paused team state survived rewind: checkpoint=%#v state=%#v", svc.teamResume, svc.teamState)
	}
	if forked := svc.GetSessionMessages(); len(forked) != 0 {
		t.Fatalf("edited resend kept superseded messages: %#v", forked)
	}
}
