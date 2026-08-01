package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type approvalProgressProbeTool struct{}

func (approvalProgressProbeTool) Name() string        { return "run_command" }
func (approvalProgressProbeTool) Description() string { return "approval progress probe" }
func (approvalProgressProbeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (approvalProgressProbeTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Summary: "executed"}, nil
}

func TestApprovalBrokerApproveReject(t *testing.T) {
	b := newApprovalBroker()
	var got ApprovalRequest
	b.SetNotify(func(req ApprovalRequest) {
		got = req
		// 模拟前端异步批准。
		go func() {
			time.Sleep(10 * time.Millisecond)
			_ = b.respond(req.ID, req.Tool, ApprovalDecision{Approved: true, Scope: "once"})
		}()
	})
	dec, err := b.request(context.Background(), ApprovalRequest{Tool: "write_file", Summary: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Approved {
		t.Fatal("应被批准")
	}
	if got.Tool != "write_file" {
		t.Fatalf("notify 收到的工具 = %q", got.Tool)
	}
}

func TestApprovalSessionAllowlist(t *testing.T) {
	b := newApprovalBroker()
	b.SetNotify(func(req ApprovalRequest) {
		go func() {
			_ = b.respond(req.ID, req.Tool, ApprovalDecision{Approved: true, Scope: "session"})
		}()
	})
	if _, err := b.request(context.Background(), ApprovalRequest{Tool: "write_file"}); err != nil {
		t.Fatal(err)
	}
	if !b.sessionAllowed("write_file") {
		t.Fatal("session 批准后应进入白名单")
	}
	if b.sessionAllowed("apply_patch") {
		t.Fatal("其他工具不应在白名单")
	}
}

func TestApprovalSessionAllowlistIsBoundToOperationArguments(t *testing.T) {
	b := newApprovalBroker()
	b.SetNotify(func(req ApprovalRequest) {
		go func() { _ = b.respond(req.ID, req.Tool, ApprovalDecision{Approved: true, Scope: "session"}) }()
	})
	args := json.RawMessage(`{"path":"one.txt","content":"one"}`)
	if _, err := b.request(context.Background(), ApprovalRequest{Tool: "write_file", Fingerprint: approvalFingerprint("write_file", args)}); err != nil {
		t.Fatal(err)
	}
	if !b.sessionAllowedFor("write_file", args) {
		t.Fatal("exact operation should be allowed")
	}
	other := json.RawMessage(`{"path":"two.txt","content":"two"}`)
	if b.sessionAllowedFor("write_file", other) {
		t.Fatal("different operation must not inherit session approval")
	}
}

func TestApprovalContextCancel(t *testing.T) {
	b := newApprovalBroker()
	b.SetNotify(func(req ApprovalRequest) {}) // 永不回应
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := b.request(ctx, ApprovalRequest{Tool: "write_file"})
	if err == nil {
		t.Fatal("ctx 超时应返回错误")
	}
}

func TestApprovalGateEmitsWaitingAndResumesAfterApproval(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "on-request"
	svc.SetApprovalNotify(func(req ApprovalRequest) {
		go func() { _ = svc.RespondApproval(req.ID, req.Tool, true, "once") }()
	})
	statuses := make([]string, 0, 2)
	ctx := tools.WithProgressSink(context.Background(), func(part tools.ResultPart) {
		statuses = append(statuses, part.Status)
	})
	result, err := svc.runToolWithApproval(ctx, approvalProgressProbeTool{}, "run_command", json.RawMessage(`{"command":"echo ok"}`))
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(statuses) != 2 || statuses[0] != "waiting" || statuses[1] != "running" {
		t.Fatalf("approval progress statuses = %#v", statuses)
	}
}

func TestApprovalGateCancellationLeavesWaitingPrompt(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "on-request"
	svc.SetApprovalNotify(func(ApprovalRequest) {})
	statuses := make([]string, 0, 1)
	baseCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	ctx := tools.WithProgressSink(baseCtx, func(part tools.ResultPart) {
		statuses = append(statuses, part.Status)
	})
	_, err := svc.runToolWithApproval(ctx, approvalProgressProbeTool{}, "run_command", json.RawMessage(`{"command":"echo ok"}`))
	if err == nil {
		t.Fatal("cancelled approval must return an error")
	}
	if len(statuses) != 1 || statuses[0] != "waiting" {
		t.Fatalf("approval progress statuses = %#v", statuses)
	}
}

func TestApprovalNoNotifyDenies(t *testing.T) {
	b := newApprovalBroker()
	// 未设置 notify（无头场景）应默认拒绝。
	dec, err := b.request(context.Background(), ApprovalRequest{Tool: "write_file"})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Approved {
		t.Fatal("无前端时应默认拒绝")
	}
}

// TestApprovalGateRejectDoesNotWrite 验证拒绝审批时文件不被落盘。
func TestApprovalGateRejectDoesNotWrite(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "guard.txt")
	_ = tools.WriteFileTextAtomic(target, "original\n", tools.FileText{LineEnding: tools.LineEndingLF})

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "on-request"
	// notify 立即拒绝。
	svc.SetApprovalNotify(func(req ApprovalRequest) {
		go func() { _ = svc.RespondApproval(req.ID, req.Tool, false, "once") }()
	})

	tool := tools.WriteFileTool{Policy: svc.sandboxPolicy()}
	args, _ := json.Marshal(map[string]string{"path": "guard.txt", "content": "hacked\n"})
	result, err := svc.runToolWithApproval(context.Background(), tool, "write_file", args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("拒绝后结果应标记为错误/拒绝")
	}
	// 文件应保持原样。
	back, _ := tools.ReadFileText(target)
	if back.Content != "original\n" {
		t.Fatalf("拒绝后文件不应改变, got %q", back.Content)
	}
}

// TestApprovalGateApproveWrites 验证批准后文件正常落盘。
func TestApprovalGateApproveWrites(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "ok.txt")

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "on-request"
	svc.SetApprovalNotify(func(req ApprovalRequest) {
		go func() { _ = svc.RespondApproval(req.ID, req.Tool, true, "once") }()
	})

	tool := tools.WriteFileTool{Policy: svc.sandboxPolicy()}
	args, _ := json.Marshal(map[string]string{"path": "ok.txt", "content": "approved\n"})
	result, err := svc.runToolWithApproval(context.Background(), tool, "write_file", args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("批准后不应报错: %s", result.Summary)
	}
	back, _ := tools.ReadFileText(target)
	if back.Content != "approved\n" {
		t.Fatalf("批准后文件应落盘, got %q", back.Content)
	}
}

// TestApprovalNeverPolicySkipsGate 验证 never 策略不弹审批、直接落盘。
func TestApprovalNeverPolicySkipsGate(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "auto.txt")

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = workspace
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	svc.runtimeSettings.ApprovalPolicy = "never"
	notified := false
	svc.SetApprovalNotify(func(req ApprovalRequest) { notified = true })

	tool := tools.WriteFileTool{Policy: svc.sandboxPolicy()}
	args, _ := json.Marshal(map[string]string{"path": "auto.txt", "content": "auto\n"})
	if _, err := svc.runToolWithApproval(context.Background(), tool, "write_file", args); err != nil {
		t.Fatal(err)
	}
	if notified {
		t.Fatal("never 策略不应触发审批通知")
	}
	back, _ := tools.ReadFileText(target)
	if back.Content != "auto\n" {
		t.Fatalf("never 策略应直接落盘, got %q", back.Content)
	}
}

func TestOnFailurePolicyIsNotEquivalentToNever(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.ApprovalPolicy = "on-failure"
	if !svc.needsApproval("run_command", json.RawMessage(`{"command":"go test ./..."}`)) {
		t.Fatal("on-failure must gate side-effecting commands")
	}
	if !svc.needsApproval("write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`)) {
		t.Fatal("on-failure must gate file mutations")
	}
	if !svc.needsApproval("ssh", json.RawMessage(`{"action":"run","credential_id":"ssh-example","command":"systemctl restart app"}`)) {
		t.Fatal("on-failure must gate remote SSH operations")
	}
}
