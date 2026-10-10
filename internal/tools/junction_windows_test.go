//go:build windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Directory junctions are available on ordinary Windows accounts even when
// symbolic-link creation is unavailable, so this escape must be tested too.
func TestWorkspaceRejectsDirectoryJunctionEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "outside-link")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create owned junction: %v (%s)", err, output)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	if _, err := policy.ResolveReadPath(filepath.Join(junction, "secret.txt")); !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Fatalf("junction read escaped workspace: %v", err)
	}
	if _, err := policy.ResolveWritePath(filepath.Join(junction, "new.txt")); !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Fatalf("junction write escaped workspace: %v", err)
	}
	// Explicitly authorized aliases remain usable, including missing children.
	policy.ExtraWritableRoots = []string{outside}
	if _, err := policy.ResolveWritePath(filepath.Join(junction, "nested", "new.txt")); err != nil {
		t.Fatalf("authorized junction target was rejected: %v", err)
	}
	dangling := filepath.Join(root, "dangling-link")
	command = exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", dangling, filepath.Join(outside, "missing-target"))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create owned dangling junction: %v (%s)", err, output)
	}
	policy.ExtraWritableRoots = nil
	if _, err := policy.ResolveWritePath(filepath.Join(dangling, "new.txt")); err == nil {
		t.Fatal("dangling junction was treated as an ordinary missing directory")
	}
}
