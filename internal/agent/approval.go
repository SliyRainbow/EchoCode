package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// 审批中介：工具循环遇到需审批的操作时，构造 ApprovalRequest 交给 notify（前端弹窗），
// 并阻塞在该请求的 channel 上等待用户决定。Wails 每个 JS→Go 调用独立 goroutine，
// 故 SendChatMessage 阻塞等待时，前端仍可调用 RespondApproval 唤醒。

// ApprovalRequest 是一次待审批的操作（发给前端展示）。
type ApprovalRequest struct {
	ID          string                 `json:"id"`
	Tool        string                 `json:"tool"`
	Kind        string                 `json:"kind"` // "file" | "command"
	Path        string                 `json:"path,omitempty"`
	Command     string                 `json:"command,omitempty"`
	URL         string                 `json:"url,omitempty"`
	Summary     string                 `json:"summary"`
	Parts       []eventlog.MessagePart `json:"parts,omitempty"` // 复用前端渲染（diff 卡片）
	Fingerprint string                 `json:"fingerprint,omitempty"`
}

// ApprovalDecision 是用户对某次审批的答复。
type ApprovalDecision struct {
	Approved bool
	// Scope: "once"=仅本次；"session"=本会话内的相同操作指纹。
	Scope string
}

// approvalBroker 管理待决审批请求。并发安全。
type approvalBroker struct {
	mu       sync.Mutex
	pending  map[string]pendingApproval
	allowAll map[string]bool // Session approvals are keyed by tool + exact operation.
	notify   func(ApprovalRequest)
	seq      int
}

type pendingApproval struct {
	channel     chan ApprovalDecision
	fingerprint string
}

var globalApprovalSequence atomic.Uint64

func newApprovalBroker() *approvalBroker {
	return &approvalBroker{
		pending:  make(map[string]pendingApproval),
		allowAll: make(map[string]bool),
	}
}

// SetNotify 注入前端通知回调（app.go 用 Wails EventsEmit 实现）。
func (b *approvalBroker) SetNotify(fn func(ApprovalRequest)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notify = fn
}

func (b *approvalBroker) notification() func(ApprovalRequest) {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notify
}

// hasNotify 是否已配置前端通知（无则表示无头/测试环境）。
func (b *approvalBroker) hasNotify() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notify != nil
}

// sessionAllowed is the legacy empty-argument fingerprint lookup.
func (b *approvalBroker) sessionAllowed(tool string) bool {
	return b.sessionAllowedFor(tool, nil)
}

func (b *approvalBroker) sessionAllowedFor(tool string, rawArgs json.RawMessage) bool {
	return b.fingerprintAllowed(approvalFingerprint(tool, rawArgs))
}

func (b *approvalBroker) fingerprintAllowed(fingerprint string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allowAll[fingerprint]
}

// request 发起一次审批并阻塞等待结果（受 ctx 取消/超时约束）。
func (b *approvalBroker) request(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	b.mu.Lock()
	b.seq++
	req.ID = fmt.Sprintf("apr-%d-%d-%d", b.seq, time.Now().UnixNano(), globalApprovalSequence.Add(1))
	ch := make(chan ApprovalDecision, 1)
	if req.Fingerprint == "" {
		req.Fingerprint = approvalFingerprint(req.Tool, nil)
	}
	b.pending[req.ID] = pendingApproval{channel: ch, fingerprint: req.Fingerprint}
	notify := b.notify
	b.mu.Unlock()

	if notify == nil {
		// 没有前端可通知（如无头/测试）：默认拒绝，避免误落盘。
		b.clear(req.ID)
		return ApprovalDecision{Approved: false, Scope: "once"}, nil
	}
	notify(req)

	select {
	case <-ctx.Done():
		b.clear(req.ID)
		return ApprovalDecision{}, ctx.Err()
	case decision := <-ch:
		b.clear(req.ID)
		if decision.Approved && decision.Scope == "session" {
			b.mu.Lock()
			b.allowAll[req.Fingerprint] = true
			b.mu.Unlock()
		}
		return decision, nil
	}
}

func (b *approvalBroker) clear(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

// respond 投递用户决定到对应请求的 channel。tool 用于会话白名单登记。
func (b *approvalBroker) respond(id string, tool string, decision ApprovalDecision) error {
	b.mu.Lock()
	pending, ok := b.pending[id]
	if ok && decision.Approved && decision.Scope == "session" {
		b.allowAll[pending.fingerprint] = true
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("审批请求不存在或已处理: %s", id)
	}
	pending.channel <- decision
	return nil
}

// resetSessionAllow 清空会话白名单（新会话时调用）。
func (b *approvalBroker) resetSessionAllow() {
	b.mu.Lock()
	b.allowAll = make(map[string]bool)
	b.mu.Unlock()
}

// --- Service 对外接口 ---

// SetApprovalNotify 注入前端通知回调（由 app.go 用 Wails EventsEmit 实现）。
func (s *Service) SetApprovalNotify(fn func(ApprovalRequest)) {
	if s.approvals != nil {
		s.approvals.SetNotify(fn)
	}
}

// RespondApproval 由前端在用户点击审批后调用。
// approved=是否批准；scope="once"|"session"。
func (s *Service) RespondApproval(id string, tool string, approved bool, scope string) error {
	if s.approvals == nil {
		return fmt.Errorf("审批中介未初始化")
	}
	if scope == "" {
		scope = "once"
	}
	return s.approvals.respond(id, tool, ApprovalDecision{Approved: approved, Scope: scope})
}

// needsApproval applies the configured host policy. Hard sandbox checks still
// run when policy=never; approval never grants a capability disabled by policy.
//
// 只读工具（read_file/list_dir/search）永不需要审批。
func (s *Service) needsApproval(toolName string, rawArgs json.RawMessage) bool {
	policy := s.runtimeSettings.ApprovalPolicy
	if policy == "never" {
		return false
	}
	switch toolName {
	case "write_file", "apply_patch", "copy_file", "delete_file", "download_file", "git_repository", "run_command", "ssh":
		return true
	case "git":
		return gitActionNeedsApproval(rawArgs)
	case "terminal":
		return terminalActionNeedsApproval(rawArgs)
	case "browser":
		return browserNavigationNeedsApproval(rawArgs)
	case "computer":
		return computerActionNeedsApproval(rawArgs)
	default:
		return strings.HasPrefix(toolName, "mcp__") || strings.HasPrefix(toolName, "plugin__")
	}
}

// runToolWithApproval 执行工具，必要时插入审批闸门。
//   - 变更类工具（实现 MutatingTool）：Preview 算 diff → 审批 → 批准才 Apply 落盘；拒绝则不落盘。
//   - 其他需审批工具（如 run_command）：审批通过才 Execute。
//   - 无需审批：直接 Execute。
func (s *Service) runToolWithApproval(ctx context.Context, tool tools.Tool, name string, rawArgs json.RawMessage) (tools.Result, error) {
	if result, handled, err := s.runBoundaryFileTool(ctx, tool, rawArgs); handled {
		return result, err
	}
	requiresApproval := s.needsApproval(name, rawArgs)
	if readOnly, ok := tool.(interface{ ReadOnly() bool }); ok && readOnly.ReadOnly() && !strings.HasPrefix(name, "mcp__") {
		requiresApproval = false
	}
	if !requiresApproval || (s.approvals != nil && s.approvals.sessionAllowedFor(name, rawArgs)) {
		return tool.Execute(ctx, rawArgs)
	}
	if s.approvals == nil {
		return rejectedResult(name, "approval is required but no approval broker is available"), nil
	}

	// 变更类工具：先预演拿到 diff，再审批。
	if mut, ok := tool.(tools.MutatingTool); ok {
		result, pending, err := mut.Preview(ctx, rawArgs)
		if err != nil {
			return result, err
		}
		if pending == nil {
			// 无实际改动或校验失败，无需审批，直接返回预演结果。
			return result, nil
		}
		emitApprovalProgress(ctx, name, rawArgs, "waiting", "等待用户确认文件修改")
		decision, err := s.approvals.request(ctx, ApprovalRequest{
			Tool:        name,
			Kind:        "file",
			Path:        pending.Change.Path,
			Summary:     result.Summary,
			Parts:       toEventParts(result.Parts),
			Fingerprint: approvalFingerprint(name, rawArgs),
		})
		if err != nil {
			return tools.Result{}, err
		}
		if !decision.Approved {
			return rejectedResult(name, "用户拒绝了对 "+pending.Change.Path+" 的修改"), nil
		}
		emitApprovalProgress(ctx, name, rawArgs, "running", "审批已通过，正在写入文件")
		if applyErr := tools.ApplyPending(pending); applyErr != nil {
			return rejectedResult(name, "写入失败: "+applyErr.Error()), nil
		}
		return tools.CompletePending(result, pending), nil
	}

	// 非变更类但需审批（run_command）：审批通过才执行。
	request := ApprovalRequest{Tool: name, Kind: "command", Command: commandFromArgs(rawArgs), Summary: "请求执行命令", Fingerprint: approvalFingerprint(name, rawArgs)}
	if name == "browser" {
		request.Kind = "browser"
		request.URL = browserURLFromArgs(rawArgs)
		request.Command = ""
		request.Summary = "请求在内置浏览器中打开外部网站"
	} else if name == "computer" {
		request.Kind = "computer"
		request.Command = truncateApprovalArgs(rawArgs)
		request.Summary = "请求操控其他应用窗口"
	} else if name == "git" {
		request.Kind = "git"
		request.Command = truncateApprovalArgs(rawArgs)
		request.Summary = "请求执行 Git 写操作"
	} else if name == "git_repository" {
		request.Kind = "git"
		request.Command = tools.GitRepositoryInputForDisplay(rawArgs)
		request.Summary = "请求拉取 Git 仓库"
	} else if name == "terminal" {
		request.Kind = "terminal"
		request.Command = truncateApprovalArgs(rawArgs)
		request.Summary = "请求操作持久终端"
	} else if name == "ssh" {
		request.Kind = "ssh"
		request.Command = sshToolInputForDisplay(rawArgs)
		request.Summary = "请求连接用户授权的 SSH 主机"
	} else if name == "download_file" {
		request.Kind = "file"
		request.URL, request.Path = downloadApprovalDetails(rawArgs)
		request.Command = ""
		request.Summary = "请求下载文件到 " + request.Path
	} else if strings.HasPrefix(name, "mcp__") {
		request.Kind = "mcp"
		request.Command = truncateApprovalArgs(rawArgs)
		request.Summary = "请求调用 MCP 工具 " + name
	} else if strings.HasPrefix(name, "plugin__") {
		request.Kind = "plugin"
		request.Command = truncateApprovalArgs(rawArgs)
		request.Summary = "请求调用插件写入工具 " + name
	}
	emitApprovalProgress(ctx, name, rawArgs, "waiting", "等待用户确认操作")
	decision, err := s.approvals.request(ctx, request)
	if err != nil {
		return tools.Result{}, err
	}
	if !decision.Approved {
		return rejectedResult(name, "用户拒绝了命令执行"), nil
	}
	emitApprovalProgress(ctx, name, rawArgs, "running", "审批已通过，正在执行")
	return tool.Execute(ctx, rawArgs)
}

func emitApprovalProgress(ctx context.Context, name string, rawArgs json.RawMessage, status, output string) {
	tools.EmitProgress(ctx, tools.ResultPart{
		Kind:   tools.PartToolCall,
		Name:   name,
		Status: status,
		Input:  toolInputForDisplay(name, rawArgs),
		Output: output,
	})
}

func truncateApprovalArgs(rawArgs json.RawMessage) string {
	value := strings.TrimSpace(string(rawArgs))
	if len(value) > 2000 {
		return value[:2000] + "..."
	}
	return value
}

func approvalFingerprint(tool string, rawArgs json.RawMessage) string {
	canonical := strings.TrimSpace(string(rawArgs))
	if len(rawArgs) > 0 {
		var value any
		if json.Unmarshal(rawArgs, &value) == nil {
			if encoded, err := json.Marshal(value); err == nil {
				canonical = string(encoded)
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(tool) + "\x00" + canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func rejectedResult(name, msg string) tools.Result {
	return tools.Result{
		Summary: msg,
		IsError: true,
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: name, Status: "error", Output: msg}},
	}
}

func commandFromArgs(rawArgs json.RawMessage) string {
	return tools.RunCommandInputForDisplay(rawArgs)
}

func downloadApprovalDetails(rawArgs json.RawMessage) (downloadURL, destination string) {
	var args struct {
		URL                  string `json:"url"`
		Destination          string `json:"destination"`
		DestinationDirectory string `json:"destination_directory"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	return tools.SafeDownloadURLForDisplay(args.URL), firstNonEmpty(strings.TrimSpace(args.Destination), strings.TrimSpace(args.DestinationDirectory))
}

func browserURLFromArgs(rawArgs json.RawMessage) string {
	var args struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	return tools.SafeDownloadURLForDisplay(args.URL)
}

func browserNavigationNeedsApproval(rawArgs json.RawMessage) bool {
	var args struct {
		Action string `json:"action"`
		URL    string `json:"url"`
	}
	if json.Unmarshal(rawArgs, &args) != nil || !strings.EqualFold(strings.TrimSpace(args.Action), "open") {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(args.URL))
	if err != nil {
		return true
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func computerActionNeedsApproval(rawArgs json.RawMessage) bool {
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(rawArgs, &args) != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list_windows":
		return false
	default:
		return true
	}
}

func gitActionNeedsApproval(rawArgs json.RawMessage) bool {
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(rawArgs, &args) != nil {
		return true
	}
	return gitActionMutates(strings.ToLower(strings.TrimSpace(args.Action)))
}

func terminalActionNeedsApproval(rawArgs json.RawMessage) bool {
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(rawArgs, &args) != nil {
		return true
	}
	return strings.ToLower(strings.TrimSpace(args.Action)) != "state"
}
