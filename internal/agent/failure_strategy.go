package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	failureStrategyStateVersion = 1
	maxFailureStrategyRecords   = 24
	maxFailureStrategyTurnAge   = 6
	contextFailureStrategyKind  = "failure-strategy-context"
	failureStrategyContextStart = "[EchoCode recent tool failure context]"
	failureStrategyContextEnd   = "[/EchoCode recent tool failure context]"
)

type failureStrategyState struct {
	Revision         int
	ProgressRevision int
	Records          []failureStrategyRecord
}

type failureStrategyRecord struct {
	Fingerprint      string
	StrategyKey      string
	Tool             string
	Category         string
	FailureClass     string
	ExitCode         *int
	Attempts         int
	BlockedAttempts  int
	ProgressRevision int
	FirstTurn        int
	LastTurn         int
	Summary          string
	Alternatives     []string
	Retryable        bool
}

type toolFailureDiagnosis struct {
	Class          string
	ExitCode       *int
	Summary        string
	Retryable      bool
	RequiredChange string
	Alternatives   []string
}

type toolRetryDiagnostic struct {
	Kind             string   `json:"kind"`
	Action           string   `json:"action"`
	Tool             string   `json:"tool"`
	FailureClass     string   `json:"failureClass"`
	ExitCode         *int     `json:"exitCode,omitempty"`
	Attempts         int      `json:"attempts"`
	Retryable        bool     `json:"retryable"`
	Summary          string   `json:"summary"`
	RequiredChange   string   `json:"requiredChange"`
	RecommendedTools []string `json:"recommendedTools,omitempty"`
}

func cloneFailureStrategyState(state failureStrategyState) failureStrategyState {
	cloned := state
	cloned.Records = make([]failureStrategyRecord, len(state.Records))
	for index, record := range state.Records {
		cloned.Records[index] = record
		if record.ExitCode != nil {
			value := *record.ExitCode
			cloned.Records[index].ExitCode = &value
		}
		cloned.Records[index].Alternatives = append([]string(nil), record.Alternatives...)
	}
	return cloned
}

func (s *Service) failureStrategySnapshot() failureStrategyState {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	return cloneFailureStrategyState(s.failureStrategy)
}

func (s *Service) replaceFailureStrategyState(state failureStrategyState) {
	s.failureMu.Lock()
	s.failureStrategy = cloneFailureStrategyState(state)
	s.failureMu.Unlock()
}

func (s *Service) mergeFailureStrategyState(state failureStrategyState, resolved map[string]bool) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()

	merged := cloneFailureStrategyState(s.failureStrategy)
	if state.Revision > merged.Revision {
		merged.Revision = state.Revision
	}
	if state.ProgressRevision > merged.ProgressRevision {
		merged.ProgressRevision = state.ProgressRevision
	}
	for key := range resolved {
		merged.remove(key)
	}
	for _, incoming := range state.Records {
		index := merged.index(incoming.StrategyKey)
		if index < 0 {
			merged.Records = append(merged.Records, incoming)
			continue
		}
		current := merged.Records[index]
		if incoming.LastTurn > current.LastTurn ||
			(incoming.LastTurn == current.LastTurn && incoming.Attempts+incoming.BlockedAttempts >= current.Attempts+current.BlockedAttempts) {
			merged.Records[index] = incoming
		}
	}
	merged.prune(0)
	s.failureStrategy = merged
}

func (state *failureStrategyState) index(strategyKey string) int {
	for index := range state.Records {
		if state.Records[index].StrategyKey == strategyKey {
			return index
		}
	}
	return -1
}

func (state *failureStrategyState) remove(strategyKey string) bool {
	index := state.index(strategyKey)
	if index < 0 {
		return false
	}
	state.Records = append(state.Records[:index], state.Records[index+1:]...)
	return true
}

func (state *failureStrategyState) prune(currentTurn int) {
	filtered := state.Records[:0]
	for _, record := range state.Records {
		if record.ProgressRevision != state.ProgressRevision {
			continue
		}
		if currentTurn > 0 && record.LastTurn > 0 && currentTurn-record.LastTurn > maxFailureStrategyTurnAge {
			continue
		}
		filtered = append(filtered, record)
	}
	state.Records = filtered
	if len(state.Records) <= maxFailureStrategyRecords {
		return
	}
	sort.SliceStable(state.Records, func(left, right int) bool {
		if state.Records[left].LastTurn != state.Records[right].LastTurn {
			return state.Records[left].LastTurn < state.Records[right].LastTurn
		}
		return state.Records[left].Attempts < state.Records[right].Attempts
	})
	state.Records = append([]failureStrategyRecord(nil), state.Records[len(state.Records)-maxFailureStrategyRecords:]...)
}

func (state *failureStrategyState) observeFailure(call protocol.ToolCall, result tools.Result, turn int) (failureStrategyRecord, toolFailureDiagnosis) {
	diagnosis := diagnoseToolFailure(call, result)
	if diagnosis.Class == "cancelled" {
		return failureStrategyRecord{}, diagnosis
	}
	strategyKey := toolFailureStrategyKey(call)
	fingerprint := toolFailureFingerprint(strategyKey, diagnosis.Class, diagnosis.ExitCode)
	index := state.index(strategyKey)
	record := failureStrategyRecord{
		Fingerprint:      fingerprint,
		StrategyKey:      strategyKey,
		Tool:             strings.TrimSpace(call.Function.Name),
		Category:         toolFailureCategory(call),
		FailureClass:     diagnosis.Class,
		ExitCode:         diagnosis.ExitCode,
		Attempts:         1,
		ProgressRevision: state.ProgressRevision,
		FirstTurn:        turn,
		LastTurn:         turn,
		Summary:          diagnosis.Summary,
		Alternatives:     append([]string(nil), diagnosis.Alternatives...),
		Retryable:        diagnosis.Retryable,
	}
	if index >= 0 && state.Records[index].ProgressRevision == state.ProgressRevision {
		previous := state.Records[index]
		record.Attempts = previous.Attempts + 1
		record.BlockedAttempts = previous.BlockedAttempts
		record.FirstTurn = previous.FirstTurn
		state.Records[index] = record
	} else if index >= 0 {
		state.Records[index] = record
	} else {
		state.Records = append(state.Records, record)
	}
	state.Revision++
	state.prune(turn)
	return record, diagnosis
}

func (state *failureStrategyState) equivalentFailure(call protocol.ToolCall, turn int) (failureStrategyRecord, bool) {
	state.prune(turn)
	index := state.index(toolFailureStrategyKey(call))
	if index < 0 {
		return failureStrategyRecord{}, false
	}
	record := state.Records[index]
	if record.ProgressRevision != state.ProgressRevision {
		return failureStrategyRecord{}, false
	}
	if record.Retryable {
		return record, record.Attempts >= 2
	}
	return record, record.Attempts >= 1
}

func (state *failureStrategyState) noteBlocked(strategyKey string, turn int) failureStrategyRecord {
	index := state.index(strategyKey)
	if index < 0 {
		return failureStrategyRecord{}
	}
	state.Records[index].BlockedAttempts++
	state.Records[index].LastTurn = turn
	state.Revision++
	return state.Records[index]
}

func (state *failureStrategyState) observeSuccess(call protocol.ToolCall, result tools.Result, resolved map[string]bool) {
	strategyKey := toolFailureStrategyKey(call)
	changed := state.remove(strategyKey)
	if changed && resolved != nil {
		resolved[strategyKey] = true
	}
	if toolResultMayChangeConditions(call.Function.Name, result) {
		state.ProgressRevision++
		for _, record := range state.Records {
			if resolved != nil {
				resolved[record.StrategyKey] = true
			}
		}
		state.Records = nil
		changed = true
	}
	if changed {
		state.Revision++
	}
}

func toolResultMayChangeConditions(name string, result tools.Result) bool {
	if len(result.Changes) > 0 {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "spreadsheet_") || strings.HasPrefix(name, "document_") || strings.HasPrefix(name, "presentation_") {
		return !strings.HasSuffix(name, "_inspect") && !strings.Contains(name, "read_range")
	}
	switch name {
	case "write_file", "apply_patch", "copy_file", "delete_file", "download_file", "run_command", "terminal", "ssh", "git", "git_repository":
		return true
	default:
		return false
	}
}

func toolFailureStrategyKey(call protocol.ToolCall) string {
	value := strings.TrimSpace(call.Function.Name) + "\x00" + equivalentToolArgumentShape(call.Function.Arguments)
	return shortFailureHash(value)
}

func toolFailureFingerprint(strategyKey, failureClass string, exitCode *int) string {
	exit := ""
	if exitCode != nil {
		exit = fmt.Sprintf("%d", *exitCode)
	}
	return shortFailureHash(strategyKey + "\x00" + failureClass + "\x00" + exit)
}

func shortFailureHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:16])
}

func diagnoseToolFailure(call protocol.ToolCall, result tools.Result) toolFailureDiagnosis {
	class, exitCode := canonicalToolFailureClass(result)
	diagnosis := toolFailureDiagnosis{
		Class:        class,
		ExitCode:     exitCode,
		Summary:      failureClassSummary(class, exitCode),
		Retryable:    class == "timeout" || class == "network" || class == "rate-limit" || class == "upstream-unavailable",
		Alternatives: recommendedAlternativeTools(call, class),
	}
	diagnosis.RequiredChange = requiredStrategyChange(class)
	return diagnosis
}

func requiredStrategyChange(class string) string {
	switch class {
	case "invalid-arguments", "syntax-error":
		return "修正参数结构或改用逐参数执行；只改引号、空格或转义形式不算新方案。"
	case "access-denied", "approval-rejected", "outside-roots":
		return "先改变权限、审批或目标路径条件；在条件未变化前不得重复相同操作。"
	case "not-found", "tool-unavailable":
		return "先确认真实路径、工具可用性或创建缺失对象，再使用不同策略继续。"
	case "data-invalid":
		return "先读取并验证真实数据结构，再使用对应结构化工具修改。"
	case "timeout", "network", "rate-limit", "upstream-unavailable":
		return "允许有限退避重试；再次失败后必须切换协议、工具或验证方式。对于指定网页或仓库，不能用网络搜索摘要替代原始证据。"
	default:
		return "分析完整错误与退出码，并改变命令、输入、工具或前置条件后再执行。"
	}
}

func canonicalToolFailureClass(result tools.Result) (string, *int) {
	text := strings.ToLower(toolFailureText(result))
	var exitCode *int
	for _, part := range result.Parts {
		if part.Kind == tools.PartToolCall && part.ExitCode != nil {
			value := *part.ExitCode
			exitCode = &value
		}
	}
	classes := []struct {
		name    string
		markers []string
	}{
		{name: "cancelled", markers: []string{"context canceled", "context cancelled", "operation canceled", "已停止", "已取消"}},
		{name: "approval-rejected", markers: []string{"approval rejected", "approval denied", "审批被拒绝", "用户拒绝"}},
		{name: "outside-roots", markers: []string{"outside the allowed roots", "outside workspace", "超出工作区", "不在允许的根目录"}},
		{name: "access-denied", markers: []string{"access is denied", "permission denied", "unauthorized", "forbidden", "无权限", "拒绝访问"}},
		{name: "rate-limit", markers: []string{"rate limit", "too many requests", "http 429", "请求过于频繁"}},
		{name: "timeout", markers: []string{"deadline exceeded", "timed out", "timeout", "超时"}},
		{name: "network", markers: []string{"connection refused", "connection reset", "connection aborted", "no such host", "network is unreachable", "eof", "连接失败", "网络不可用"}},
		{name: "tool-unavailable", markers: []string{"tool is unavailable", "tool not registered", "unsupported tool", "工具不可用", "未注册工具"}},
		{name: "not-found", markers: []string{"not found", "cannot find", "does not exist", "no such file", "不存在", "找不到"}},
		{name: "syntax-error", markers: []string{"syntaxerror", "syntax error", "unterminated string", "unexpected token", "语法错误"}},
		{name: "invalid-arguments", markers: []string{"invalid argument", "invalid arguments", "cannot unmarshal", "parse error", "参数无效", "参数错误", "解析失败"}},
		{name: "data-invalid", markers: []string{"invalid data", "corrupt", "malformed", "unsupported format", "数据无效", "文件损坏", "格式不支持"}},
		{name: "upstream-unavailable", markers: []string{"service unavailable", "bad gateway", "gateway timeout", "upstream", "服务不可用"}},
	}
	for _, candidate := range classes {
		for _, marker := range candidate.markers {
			if strings.Contains(text, marker) {
				return candidate.name, exitCode
			}
		}
	}
	if exitCode != nil && *exitCode != 0 {
		return "process-exit", exitCode
	}
	return "tool-error", exitCode
}

func toolFailureText(result tools.Result) string {
	var text strings.Builder
	text.WriteString(result.Summary)
	for _, part := range result.Parts {
		if part.Kind != tools.PartToolCall {
			continue
		}
		text.WriteByte('\n')
		text.WriteString(part.Output)
		text.WriteByte('\n')
		text.WriteString(part.Stderr)
	}
	return text.String()
}

func failureClassSummary(class string, exitCode *int) string {
	summaries := map[string]string{
		"cancelled":            "操作已取消",
		"approval-rejected":    "审批未通过",
		"outside-roots":        "目标超出允许目录",
		"access-denied":        "权限不足",
		"rate-limit":           "服务限流",
		"timeout":              "操作超时",
		"network":              "网络连接失败",
		"tool-unavailable":     "工具不可用",
		"not-found":            "目标不存在",
		"syntax-error":         "命令或代码语法错误",
		"invalid-arguments":    "工具参数无效",
		"data-invalid":         "输入数据或文件格式无效",
		"upstream-unavailable": "上游服务不可用",
		"process-exit":         "进程以非零状态退出",
		"tool-error":           "工具执行失败",
	}
	summary := summaries[class]
	if summary == "" {
		summary = "工具执行失败"
	}
	if exitCode != nil {
		summary += fmt.Sprintf("（退出码 %d）", *exitCode)
	}
	return summary
}

func recommendedAlternativeTools(call protocol.ToolCall, failureClass string) []string {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	raw := strings.ToLower(string(call.Function.Arguments))
	recommendations := make([]string, 0, 6)
	add := func(names ...string) {
		for _, candidate := range names {
			if candidate == "" {
				continue
			}
			found := false
			for _, existing := range recommendations {
				if existing == candidate {
					found = true
					break
				}
			}
			if !found {
				recommendations = append(recommendations, candidate)
			}
		}
	}

	if name == "run_command" || name == "terminal" {
		if strings.Contains(raw, ".xls") {
			add("spreadsheet_inspect", "spreadsheet_create", "spreadsheet_write_range")
		}
		if strings.Contains(raw, ".docx") {
			add("document_inspect", "document_create", "document_replace_text")
		}
		if strings.Contains(raw, ".pptx") {
			add("presentation_inspect", "presentation_create", "presentation_replace_text")
		}
		if containsAny(raw, "get-content", "set-content", "add-content", "out-file", "cat ", "type ", "echo ", "copy-item", "remove-item", "move-item", "readfile", "writefile") {
			add("read_file", "write_file", "apply_patch", "copy_file", "delete_file")
		}
		if containsAny(raw, "rg ", "grep ", "findstr ", "select-string") {
			add("search")
		}
		if failureClass == "syntax-error" || failureClass == "invalid-arguments" {
			add("run_command(executable+args)")
		}
	}
	if strings.HasPrefix(name, "spreadsheet_") {
		add("spreadsheet_inspect", "spreadsheet_read_range")
	}
	if strings.HasPrefix(name, "document_") {
		add("document_inspect")
	}
	if strings.HasPrefix(name, "presentation_") {
		add("presentation_inspect")
	}
	if failureClass == "not-found" {
		add("file_info", "list_dir", "search")
	}
	if failureClass == "network" || failureClass == "upstream-unavailable" {
		switch name {
		case "read_webpage":
			add("browser")
		case "read_repository":
			add("git_repository")
		case "download_file":
			add("read_webpage", "browser")
		}
	}
	if len(recommendations) > 6 {
		recommendations = recommendations[:6]
	}
	return recommendations
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func toolFailureCategory(call protocol.ToolCall) string {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	switch {
	case name == "run_command" || name == "terminal":
		return "shell"
	case name == "ssh":
		return "remote"
	case strings.HasPrefix(name, "spreadsheet_"):
		return "spreadsheet"
	case strings.HasPrefix(name, "document_"):
		return "document"
	case strings.HasPrefix(name, "presentation_"):
		return "presentation"
	case name == "read_file" || name == "file_info" || name == "list_dir" || name == "search" || name == "write_file" || name == "apply_patch" || name == "copy_file" || name == "delete_file":
		return "filesystem"
	case name == "web_search" || name == "read_webpage" || name == "read_repository" || name == "download_file":
		return "network"
	case name == "browser":
		return "browser"
	case name == "git" || name == "git_repository":
		return "git"
	default:
		return "tool"
	}
}

func failureDiagnosticContent(action string, record failureStrategyRecord, diagnosis toolFailureDiagnosis) string {
	diagnostic := toolRetryDiagnostic{
		Kind:             "echocode_tool_retry_diagnostic",
		Action:           action,
		Tool:             record.Tool,
		FailureClass:     record.FailureClass,
		ExitCode:         record.ExitCode,
		Attempts:         record.Attempts,
		Retryable:        record.Retryable,
		Summary:          record.Summary,
		RequiredChange:   diagnosis.RequiredChange,
		RecommendedTools: append([]string(nil), record.Alternatives...),
	}
	encoded, _ := json.Marshal(diagnostic)
	return string(encoded)
}

func (g *toolLoopGuard) beforeEquivalentFailure(call protocol.ToolCall) (tools.Result, protocol.Message, bool) {
	name := strings.TrimSpace(call.Function.Name)
	switch name {
	case "", "update_plan", "delegate_task", "await_subagents":
		return tools.Result{}, protocol.Message{}, false
	}
	record, blocked := g.failureStrategy.equivalentFailure(call, g.turnIndex)
	if !blocked {
		return tools.Result{}, protocol.Message{}, false
	}
	record = g.failureStrategy.noteBlocked(record.StrategyKey, g.turnIndex)
	if g.blockedFailures == nil {
		g.blockedFailures = make(map[string]int)
	}
	g.blockedFailures[record.StrategyKey]++
	if g.blockedFailures[record.StrategyKey] >= 2 {
		g.forceFinalResponse = true
	}
	diagnosis := toolFailureDiagnosis{
		Class:          record.FailureClass,
		ExitCode:       record.ExitCode,
		Summary:        record.Summary,
		Retryable:      record.Retryable,
		RequiredChange: requiredStrategyChange(record.FailureClass),
		Alternatives:   append([]string(nil), record.Alternatives...),
	}
	diagnostic := failureDiagnosticContent("blocked_equivalent_retry", record, diagnosis)
	summary := fmt.Sprintf("已拦截 %s 的等价失败重试：%s。必须先发生实质变化。", name, record.Summary)
	result := tools.Result{
		Summary: summary,
		IsError: true,
		Parts: []tools.ResultPart{{
			Kind:   tools.PartToolCall,
			Name:   name,
			Status: "error",
			Input:  toolInputForDisplay(name, call.Function.Arguments),
			Output: summary + "\n" + diagnostic,
		}},
	}
	message := protocol.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Name:       name,
		Content:    summary + "\n\nEchoCode structured failure diagnosis:\n" + diagnostic,
	}
	return result, message, true
}

func toEventFailureStrategyState(state failureStrategyState) *eventlog.FailureStrategyState {
	converted := &eventlog.FailureStrategyState{
		Version:          failureStrategyStateVersion,
		Revision:         state.Revision,
		ProgressRevision: state.ProgressRevision,
		Records:          make([]eventlog.FailureStrategyRecord, 0, len(state.Records)),
	}
	for _, record := range state.Records {
		converted.Records = append(converted.Records, eventlog.FailureStrategyRecord{
			Fingerprint:      record.Fingerprint,
			StrategyKey:      record.StrategyKey,
			Tool:             record.Tool,
			Category:         record.Category,
			FailureClass:     record.FailureClass,
			ExitCode:         record.ExitCode,
			Attempts:         record.Attempts,
			BlockedAttempts:  record.BlockedAttempts,
			ProgressRevision: record.ProgressRevision,
			FirstTurn:        record.FirstTurn,
			LastTurn:         record.LastTurn,
			Summary:          record.Summary,
			Alternatives:     append([]string(nil), record.Alternatives...),
			Retryable:        record.Retryable,
		})
	}
	return converted
}

func fromEventFailureStrategyState(stored *eventlog.FailureStrategyState) (failureStrategyState, bool) {
	if stored == nil || stored.Version <= 0 {
		return failureStrategyState{}, false
	}
	state := failureStrategyState{
		Revision:         stored.Revision,
		ProgressRevision: stored.ProgressRevision,
		Records:          make([]failureStrategyRecord, 0, len(stored.Records)),
	}
	for _, record := range stored.Records {
		if strings.TrimSpace(record.StrategyKey) == "" || strings.TrimSpace(record.Tool) == "" {
			continue
		}
		state.Records = append(state.Records, failureStrategyRecord{
			Fingerprint:      record.Fingerprint,
			StrategyKey:      record.StrategyKey,
			Tool:             record.Tool,
			Category:         record.Category,
			FailureClass:     record.FailureClass,
			ExitCode:         record.ExitCode,
			Attempts:         record.Attempts,
			BlockedAttempts:  record.BlockedAttempts,
			ProgressRevision: record.ProgressRevision,
			FirstTurn:        record.FirstTurn,
			LastTurn:         record.LastTurn,
			Summary:          record.Summary,
			Alternatives:     append([]string(nil), record.Alternatives...),
			Retryable:        record.Retryable,
		})
	}
	state.prune(0)
	return state, true
}

func formatFailureStrategyContext(state failureStrategyState) string {
	if state.Revision <= 0 {
		return ""
	}
	state.prune(0)
	var content strings.Builder
	content.WriteString(failureStrategyContextStart)
	content.WriteString("\nThis is branch-local execution state, not a user request. Do not repeat a blocked strategy unless the diagnostic's required condition changed.\n")
	content.WriteString(fmt.Sprintf("state_revision=%d progress_revision=%d unresolved=%d\n", state.Revision, state.ProgressRevision, len(state.Records)))
	if len(state.Records) == 0 {
		content.WriteString("- No unresolved equivalent tool failures.\n")
	} else {
		for _, record := range state.Records {
			content.WriteString(fmt.Sprintf("- tool=%s category=%s class=%s attempts=%d blocked=%d retryable=%t strategy=%s",
				record.Tool, record.Category, record.FailureClass, record.Attempts, record.BlockedAttempts, record.Retryable, record.StrategyKey))
			if record.ExitCode != nil {
				content.WriteString(fmt.Sprintf(" exit_code=%d", *record.ExitCode))
			}
			if len(record.Alternatives) > 0 {
				content.WriteString(" alternatives=")
				content.WriteString(strings.Join(record.Alternatives, ","))
			}
			content.WriteByte('\n')
		}
	}
	content.WriteString(failureStrategyContextEnd)
	return content.String()
}

func stripFailureStrategyContext(content string) string {
	for {
		start := strings.Index(content, failureStrategyContextStart)
		if start < 0 {
			return strings.TrimSpace(content)
		}
		relativeEnd := strings.Index(content[start+len(failureStrategyContextStart):], failureStrategyContextEnd)
		if relativeEnd < 0 {
			return strings.TrimSpace(content[:start])
		}
		end := start + len(failureStrategyContextStart) + relativeEnd + len(failureStrategyContextEnd)
		content = strings.TrimSpace(content[:start]) + "\n" + strings.TrimSpace(content[end:])
	}
}

func stripPrivateAssistantContext(content string) string {
	return stripExecutionCheckpoint(stripPrivateTurnContext(stripFailureStrategyContext(stripLocalArtifactContext(content))))
}

func (s *Service) appendProtocolAssistantMessage(messages []protocol.Message, content string, parts []tools.ResultPart) []protocol.Message {
	messages = append(messages, s.protocolAssistantMessage(content, parts))
	if context := formatFailureStrategyContext(s.failureStrategySnapshot()); context != "" {
		messages = append(messages, protocol.Message{Role: "system", Content: context, InternalKind: contextFailureStrategyKind})
	}
	return messages
}
