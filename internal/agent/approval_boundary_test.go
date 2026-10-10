package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestBoundaryApprovalWritesWorkspaceWithoutPrompt(t *testing.T) {
	s := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer s.Close()
	s.runtimeSettings.WorkspaceRoot = t.TempDir()
	s.runtimeSettings.ApprovalPolicy = "on-request"
	var calls atomic.Int32
	s.SetApprovalNotify(func(req ApprovalRequest) {
		calls.Add(1)
		go func() { _ = s.RespondApproval(req.ID, req.Tool, false, "once") }()
	})
	result, err := s.runToolWithApproval(context.Background(), tools.WriteFileTool{Policy: s.sandboxPolicy()}, "write_file", json.RawMessage(`{"path":"ok.txt","content":"within workspace"}`))
	if err != nil || result.IsError || calls.Load() != 0 {
		t.Fatalf("workspace file unexpectedly gated: %v %+v calls=%d", err, result, calls.Load())
	}
	if data, err := os.ReadFile(filepath.Join(s.runtimeSettings.WorkspaceRoot, "ok.txt")); err != nil || string(data) != "within workspace" {
		t.Fatal("workspace write failed")
	}
}

func TestBoundaryApprovalExternalGrantIsExactAndEphemeral(t *testing.T) {
	s := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer s.Close()
	s.runtimeSettings.WorkspaceRoot = t.TempDir()
	s.runtimeSettings.ApprovalPolicy = "on-request"
	target := filepath.Join(t.TempDir(), "outside.txt")
	tool := tools.WriteFileTool{Policy: s.sandboxPolicy()}
	args, _ := json.Marshal(map[string]string{"path": target, "content": "approved only"})
	var calls atomic.Int32
	allow := false
	s.SetApprovalNotify(func(req ApprovalRequest) {
		calls.Add(1)
		if !strings.Contains(req.Path, "outside.txt") && !strings.Contains(req.Path, "sibling.txt") {
			t.Error("approval did not identify external file")
		}
		go func() { _ = s.RespondApproval(req.ID, req.Tool, allow, "session") }()
	})
	result, err := s.runToolWithApproval(context.Background(), tool, tool.Name(), args)
	if err != nil || !result.IsError {
		t.Fatal("outside write not rejected")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("rejected write touched external file")
	}
	allow = true
	result, err = s.runToolWithApproval(context.Background(), tool, tool.Name(), args)
	if err != nil || result.IsError {
		t.Fatalf("approved external write failed: %v %+v", err, result)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "approved only" {
		t.Fatal("approved target not written")
	}
	result, err = s.runToolWithApproval(context.Background(), tool, tool.Name(), args)
	if err != nil || result.IsError || calls.Load() != 2 {
		t.Fatal("exact session approval not reused")
	}
	if len(s.runtimeSettings.ExtraWritableRoots) != 0 || len(tool.Policy.ExtraWritableRoots) != 0 {
		t.Fatal("approval leaked to runtime policy")
	}
	allow = false
	other, _ := json.Marshal(map[string]string{"path": filepath.Join(filepath.Dir(target), "sibling.txt"), "content": "must not write"})
	result, _ = s.runToolWithApproval(context.Background(), tool, tool.Name(), other)
	if !result.IsError || calls.Load() != 3 {
		t.Fatal("external approval leaked to sibling")
	}
}

func TestBoundaryApprovalCannotBypassReadOnlyOrTaskScope(t *testing.T) {
	s := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer s.Close()
	s.runtimeSettings.WorkspaceRoot = t.TempDir()
	s.runtimeSettings.ApprovalPolicy = "on-request"
	p := s.sandboxPolicy()
	p.FilesystemAccess = "read-only"
	result, _ := s.runToolWithApproval(context.Background(), tools.WriteFileTool{Policy: p}, "write_file", json.RawMessage(`{"path":"blocked.txt","content":"no"}`))
	if !result.IsError {
		t.Fatal("read-only policy bypassed")
	}
	p.FilesystemAccess = "workspace-write"
	p.TaskScopeEnabled, p.TaskScopeFiles = true, []string{filepath.Join(p.WorkspaceRoot, "allowed.txt")}
	result, _ = s.runToolWithApproval(context.Background(), tools.WriteFileTool{Policy: p}, "write_file", json.RawMessage(`{"path":"blocked.txt","content":"no"}`))
	if !result.IsError {
		t.Fatal("narrower task scope bypassed")
	}
}

func TestLegacyApprovalMigrationPreservesConservativeBehaviour(t *testing.T) {
	for _, old := range []string{"on-request", "on-failure", "untrusted"} {
		settings := DefaultRuntimeSettings()
		settings.SchemaVersion, settings.ApprovalPolicy = 13, old
		if got := migrateRuntimeSettings(settings).Normalized().ApprovalPolicy; got != "always" {
			t.Fatalf("legacy %s silently broadened: %s", old, got)
		}
	}
	settings := DefaultRuntimeSettings()
	if got := migrateRuntimeSettings(settings).Normalized().ApprovalPolicy; got != "on-request" {
		t.Fatalf("new default changed: %s", got)
	}
}

func TestBoundaryApprovedExternalFileRewindsAfterRestart(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(root, "sessions"), SettingsPath: filepath.Join(root, "settings.json")}
	s := NewService(config)
	defer s.Close()
	settings := s.WorkbenchState().RuntimeSettings
	settings.WorkspaceRoot, settings.ApprovalPolicy = workspace, "on-request"
	if _, err := s.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.recordAssistantAndCheckpoint("baseline", "fixture", nil); err != nil {
		t.Fatal(err)
	}
	baseline := s.eventStore.Head()
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("BEFORE"), 0600); err != nil {
		t.Fatal(err)
	}
	s.SetApprovalNotify(func(req ApprovalRequest) { go func() { _ = s.RespondApproval(req.ID, req.Tool, true, "once") }() })
	args, _ := json.Marshal(map[string]string{"path": target, "content": "AFTER"})
	result, err := s.runToolWithApproval(context.Background(), tools.WriteFileTool{Policy: s.sandboxPolicy()}, "write_file", args)
	if err != nil || result.IsError || len(result.Changes) != 1 || result.Changes[0].ApprovedExternalPath == "" {
		t.Fatalf("missing host grant: %+v %v", result, err)
	}
	if err := s.recordFileSnapshot(result.Changes[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.recordAssistantAndCheckpoint("changed", "fixture", nil); err != nil {
		t.Fatal(err)
	}
	changed := s.eventStore.Head()
	s.Close()
	restarted := NewService(config)
	defer restarted.Close()
	if _, err := restarted.RewindToCheckpoint(baseline); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "BEFORE" {
		t.Fatal("external before-image not restored")
	}
	if _, err := restarted.SwitchBranch(changed); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "AFTER" {
		t.Fatal("external after-image not restored")
	}
	if len(restarted.runtimeSettings.ExtraWritableRoots) != 0 {
		t.Fatal("recovery changed global access")
	}
}

func TestToolJSONCannotForgeExternalApproval(t *testing.T) {
	var change tools.FileChange
	if err := json.Unmarshal([]byte(`{"path":"outside.txt","ApprovedExternalPath":"outside.txt","approvedExternalPath":"outside.txt"}`), &change); err != nil {
		t.Fatal(err)
	}
	if change.ApprovedExternalPath != "" {
		t.Fatal("tool JSON forged host approval")
	}
}
