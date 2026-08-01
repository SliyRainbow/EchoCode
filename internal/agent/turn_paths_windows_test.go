//go:build windows

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestExplicitTurnPathGrantsExtractsPathsAndDriveMentions(t *testing.T) {
	grants := explicitTurnPathGrants("download to `D:\\Build Output\\app.zip`, then create E:\\Reports\\summary.xlsx and use F drive F盘")
	want := []string{
		filepath.Clean(`D:\Build Output\app.zip`),
		filepath.Clean(`E:\Reports\summary.xlsx`),
		filepath.Clean(`F:\`),
	}
	for _, expected := range want {
		found := false
		for _, actual := range grants {
			found = found || strings.EqualFold(actual, expected)
		}
		if !found {
			t.Fatalf("grants %q do not contain %q", grants, expected)
		}
	}
}

func TestTurnPathAccessIsTemporaryAndReachesRegistry(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	workspace := t.TempDir()
	target := filepath.Join(t.TempDir(), "exports")
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	ctx := withTurnWritableRoots(context.Background(), []string{target})
	registry := service.buildToolRegistryForContext(ctx)
	tool, ok := registry.Get("write_file")
	if !ok {
		t.Fatal("write_file is not registered")
	}
	write := tool.(tools.WriteFileTool)
	if _, err := write.Policy.ResolveWritePath(filepath.Join(target, "result.txt")); err != nil {
		t.Fatalf("turn path was not authorized: %v", err)
	}
	if _, err := service.sandboxPolicy().ResolveWritePath(filepath.Join(target, "result.txt")); !errors.Is(err, tools.ErrPathOutsideWorkspace) {
		t.Fatalf("temporary authorization leaked into runtime settings: %v", err)
	}
}

func TestExplicitTurnPathGrantRejectsProtectedSystemLocation(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	_, _, err := service.prepareTurnPathAccess(context.Background(), `write C:\Windows\System32\echocode-test.txt`)
	if err == nil || !strings.Contains(err.Error(), "protected system location") {
		t.Fatalf("protected path error = %v", err)
	}
}
