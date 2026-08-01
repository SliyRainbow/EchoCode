package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestInferTurnTaskScopeRecognizesMissingNamedDirectory(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	scope := service.inferTurnTaskScope("create directory `echocode-agent-web-test` and add index.html", nil)
	expected := filepath.Join(workspace, "echocode-agent-web-test")
	if !scope.Enabled || !scope.RequireWrite || len(scope.Roots) != 1 || !sameTurnPath(scope.Roots[0], expected) || len(scope.Files) != 0 {
		t.Fatalf("scope = %#v", scope)
	}
}

func TestInferTurnTaskScopeKeepsBareOutputFilesInsideNamedDirectory(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	scope := service.inferTurnTaskScope("create directory `echocode-agent-web-test` and add `index.html` and `styles.css`", nil)
	expected := filepath.Join(workspace, "echocode-agent-web-test")
	if !scope.Enabled || len(scope.Roots) != 1 || !sameTurnPath(scope.Roots[0], expected) || len(scope.Files) != 0 {
		t.Fatalf("bare output names escaped the named directory: %#v", scope)
	}
}

func TestInferTurnTaskScopeDoesNotTreatModelNameAsPath(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	scope := service.inferTurnTaskScope("use model `claude-opus-5` for the review", nil)
	if scope.Enabled || len(scope.Roots) != 0 || len(scope.Files) != 0 {
		t.Fatalf("model name became a task scope: %#v", scope)
	}
}

func TestInferTurnTaskScopeStillAppliesWithUnrestrictedFilesystem(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "unrestricted"

	scope := service.inferTurnTaskScope("create directory `echocode-agent-web-test` and add index.html", nil)
	if !scope.Enabled || len(scope.Roots) != 1 || !sameTurnPath(scope.Roots[0], filepath.Join(workspace, "echocode-agent-web-test")) {
		t.Fatalf("unrestricted profile disabled the explicit task scope: %#v", scope)
	}
}

func TestInferTurnTaskScopeIncludesExplicitExternalTarget(t *testing.T) {
	workspace := t.TempDir()
	external := filepath.Join(t.TempDir(), "exports")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"

	scope := service.inferTurnTaskScope("write the export", []string{external})
	if !scope.Enabled || len(scope.Roots) != 1 || !sameTurnPath(scope.Roots[0], external) {
		t.Fatalf("external scope = %#v", scope)
	}
	ctx := withTurnWritableRoots(context.Background(), []string{external})
	ctx = withTurnTaskScope(ctx, scope)
	registry := service.buildToolRegistryForContext(ctx)
	writeTool, ok := registry.Get("write_file")
	if !ok {
		t.Fatal("write_file is not registered")
	}
	policy := writeTool.(tools.WriteFileTool).Policy
	if _, err := policy.ResolveWritePath(filepath.Join(external, "result.txt")); err != nil {
		t.Fatalf("external target was not propagated to the tool registry: %v", err)
	}
	if _, err := policy.ResolveWritePath(filepath.Join(workspace, "unrelated.txt")); err == nil {
		t.Fatal("task scope did not reject an unrelated workspace write")
	}
}

func TestTurnTaskScopeRejectsPseudoSuccessWithoutArtifact(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	scope := turnTaskScope{
		Enabled: true, RequireWrite: true,
		Roots: []string{filepath.Join(workspace, "target")},
	}
	ctx := withTurnTaskScope(context.Background(), scope)
	if err := service.validateTurnTaskScopeOutcome(ctx, toolLoopOutcome{Content: "done"}); err == nil {
		t.Fatal("pseudo-success without a target artifact was accepted")
	}
	if err := service.validateTurnTaskScopeOutcome(ctx, toolLoopOutcome{
		Changes: []tools.FileChange{{Path: "target/index.html"}},
	}); err != nil {
		t.Fatalf("verified target change was rejected: %v", err)
	}
}

func TestTurnTaskScopeIsAddedToPrivateModelContext(t *testing.T) {
	workspace := t.TempDir()
	scope := turnTaskScope{
		Enabled: true, RequireWrite: true,
		Roots: []string{filepath.Join(workspace, "echocode-agent-web-test")},
	}
	preview := withTurnTaskScopeContext(RequestContext{}, scope, workspace)
	contextText := formatPrivateTurnContext(preview)
	for _, expected := range []string{"[task_scope]", "echocode-agent-web-test", "目录授权包含全部后代", "不得修改未列出的文件", "真实文件变更"} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("private task scope context is missing %q: %s", expected, contextText)
		}
	}
}

func TestTurnTaskScopeKeepsReadOnlyGitButHidesPersistentTerminal(t *testing.T) {
	workspace := t.TempDir()
	service := NewService(ServiceConfig{
		SkillsDir: t.TempDir(),
		Git:       gitControllerStub{},
		Terminal:  &terminalControllerStub{},
	})
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.ShellAccess = true
	ctx := withTurnTaskScope(context.Background(), turnTaskScope{
		Enabled: true,
		Roots:   []string{filepath.Join(workspace, "echocode-agent-web-test")},
	})
	registry := service.buildToolRegistryForContext(ctx)
	gitTool, ok := registry.Get("git")
	if !ok || !gitTool.(GitTool).ReadOnlyOnly {
		t.Fatal("scoped turn should retain read-only Git inspection")
	}
	if _, ok := registry.Get("terminal"); ok {
		t.Fatal("persistent terminal leaked into a scoped turn")
	}
	if _, ok := registry.Get("run_command"); !ok {
		t.Fatal("scoped run_command should remain available with its strict working-directory guard")
	}
}
