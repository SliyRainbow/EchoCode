package workspacegit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceStatusDiffStageCommitAndBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestGit(t, root, "init")
	runTestGit(t, root, "config", "user.email", "echocode@example.invalid")
	runTestGit(t, root, "config", "user.name", "EchoCode Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", "tracked.txt")
	runTestGit(t, root, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := Service{}
	status, err := service.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Clean || status.ModifiedCount != 1 || status.UntrackedCount != 1 {
		t.Fatalf("status = %#v", status)
	}
	initialBranch := status.Branch
	diff, err := service.Diff(context.Background(), root, "tracked.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "-before") || !strings.Contains(diff.Patch, "+after") {
		t.Fatalf("diff = %q", diff.Patch)
	}
	untrackedDiff, err := service.Diff(context.Background(), root, "new.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(untrackedDiff.Patch, "+new file") {
		t.Fatalf("untracked diff = %q", untrackedDiff.Patch)
	}

	status, err = service.Stage(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.StagedCount != 2 {
		t.Fatalf("staged count = %d", status.StagedCount)
	}
	stagedDiff, err := service.Diff(context.Background(), root, "tracked.txt", true)
	if err != nil || !strings.Contains(stagedDiff.Patch, "+after") {
		t.Fatalf("staged diff = %q, err=%v", stagedDiff.Patch, err)
	}
	status, err = service.Unstage(context.Background(), root, []string{"new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if status.StagedCount != 1 || status.UntrackedCount != 1 {
		t.Fatalf("status after unstage = %#v", status)
	}
	status, err = service.Stage(context.Background(), root, []string{"new.txt"})
	if err != nil || status.StagedCount != 2 {
		t.Fatalf("status after restage = %#v, err=%v", status, err)
	}
	status, err = service.Commit(context.Background(), root, "update files")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Clean {
		t.Fatalf("status after commit = %#v", status)
	}
	status, err = service.CreateBranch(context.Background(), root, "echocode/test-branch")
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "echocode/test-branch" {
		t.Fatalf("branch = %q", status.Branch)
	}
	status, err = service.SwitchBranch(context.Background(), root, initialBranch)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != initialBranch {
		t.Fatalf("switched branch = %q, want %q", status.Branch, initialBranch)
	}
}

func TestServiceRejectsRepositoryOutsideWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestGit(t, root, "init")
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (Service{}).Status(context.Background(), child)
	if err == nil || !strings.Contains(err.Error(), "workspace must be the repository root") {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceUnstagesFilesBeforeFirstCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{}
	status, err := service.Stage(context.Background(), root, []string{"first.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if status.StagedCount != 1 {
		t.Fatalf("staged count = %d", status.StagedCount)
	}
	status, err = service.Unstage(context.Background(), root, []string{"first.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if status.StagedCount != 0 || status.UntrackedCount != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestDiffWithOptionsIgnoresWhitespace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestGit(t, root, "init")
	runTestGit(t, root, "config", "user.email", "echocode@example.invalid")
	runTestGit(t, root, "config", "user.name", "EchoCode Test")
	path := filepath.Join(root, "spacing.txt")
	if err := os.WriteFile(path, []byte("value = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", "spacing.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("value    =    1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{}
	normal, err := service.DiffWithOptions(context.Background(), root, "spacing.txt", DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := service.DiffWithOptions(context.Background(), root, "spacing.txt", DiffOptions{IgnoreWhitespace: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(normal.Patch) == "" || strings.TrimSpace(ignored.Patch) != "" {
		t.Fatalf("normal=%q ignored=%q", normal.Patch, ignored.Patch)
	}
}

func TestServiceCreatesPermanentWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "init")
	runTestGit(t, repository, "config", "user.email", "echocode@example.invalid")
	runTestGit(t, repository, "config", "user.name", "EchoCode Test")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("worktree fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "tracked.txt")
	runTestGit(t, repository, "commit", "-m", "initial")

	destination := filepath.Join(base, "feature-worktree")
	created, err := (Service{}).CreateWorktree(context.Background(), repository, "echocode/permanent", destination)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(created.Path, destination) || created.Branch != "echocode/permanent" || !samePath(created.RepositoryRoot, repository) {
		t.Fatalf("created worktree = %#v", created)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "tracked.txt")); err != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != "worktree fixture\n" {
		t.Fatalf("worktree checkout data=%q err=%v", data, err)
	}
	status, err := (Service{}).Status(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Branch != "echocode/permanent" {
		t.Fatalf("worktree status = %#v", status)
	}
	if _, err := (Service{}).CreateWorktree(context.Background(), repository, "echocode/inside", filepath.Join(repository, "nested-worktree")); err == nil {
		t.Fatal("worktree inside the source repository should be rejected")
	}
}

func runTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
