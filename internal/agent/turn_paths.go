package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type turnWritableRootsKey struct{}
type turnTaskScopeKey struct{}

// turnTaskScope is intentionally ephemeral. It narrows the normal project
// sandbox for one user turn when the request names a concrete target, such as
// a new directory or an explicitly named file. It is passed through context so
// detached runtimes and subagents inherit the same boundary without changing
// the project's saved permission settings.
type turnTaskScope struct {
	Enabled      bool
	RequireWrite bool
	Roots        []string
	Files        []string
}

func withTurnWritableRoots(ctx context.Context, roots []string) context.Context {
	if len(roots) == 0 {
		return ctx
	}
	return context.WithValue(ctx, turnWritableRootsKey{}, append([]string(nil), roots...))
}

func turnWritableRoots(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	roots, _ := ctx.Value(turnWritableRootsKey{}).([]string)
	return append([]string(nil), roots...)
}

func withTurnTaskScope(ctx context.Context, scope turnTaskScope) context.Context {
	if !scope.Enabled {
		return ctx
	}
	scope.Roots = append([]string(nil), scope.Roots...)
	scope.Files = append([]string(nil), scope.Files...)
	return context.WithValue(ctx, turnTaskScopeKey{}, scope)
}

func turnTaskScopeFrom(ctx context.Context) turnTaskScope {
	if ctx == nil {
		return turnTaskScope{}
	}
	scope, _ := ctx.Value(turnTaskScopeKey{}).(turnTaskScope)
	scope.Roots = append([]string(nil), scope.Roots...)
	scope.Files = append([]string(nil), scope.Files...)
	return scope
}

func (s *Service) prepareTurnPathAccess(ctx context.Context, prompt string) (context.Context, []string, error) {
	roots := explicitTurnPathGrants(prompt)
	if !strings.EqualFold(strings.TrimSpace(s.runtimeSettings.FilesystemAccess), "unrestricted") {
		for _, root := range roots {
			if err := validateExplicitTurnPathGrant(root); err != nil {
				return ctx, nil, err
			}
		}
	}
	scope := s.inferTurnTaskScope(prompt, roots)
	ctx = withTurnWritableRoots(ctx, roots)
	ctx = withTurnTaskScope(ctx, scope)
	return ctx, roots, nil
}

func (s *Service) sandboxPolicyForContext(ctx context.Context) tools.SandboxPolicy {
	policy := s.sandboxPolicy()
	policy.ExtraWritableRoots = mergeTurnRoots(policy.ExtraWritableRoots, turnWritableRoots(ctx))
	scope := turnTaskScopeFrom(ctx)
	policy.TaskScopeEnabled = scope.Enabled
	policy.TaskScopeRoots = append([]string(nil), scope.Roots...)
	policy.TaskScopeFiles = append([]string(nil), scope.Files...)
	return policy
}

func withTurnTaskScopeContext(preview RequestContext, scope turnTaskScope, workspace string) RequestContext {
	if !scope.Enabled {
		return preview
	}
	formatPath := func(path string) string {
		path = filepath.Clean(path)
		if root, err := filepath.Abs(filepath.Clean(workspace)); err == nil {
			if relative, relErr := filepath.Rel(root, path); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return filepath.ToSlash(relative)
			}
		}
		return filepath.ToSlash(path)
	}
	lines := []string{
		"用户已明确授权以下本轮目标：目录授权包含全部后代，文件授权仅包含该文件。",
		"可读取已配置工作区中的相关资料作为参考，但不得修改未列出的文件，也不得用已有兄弟项目代替目标。",
	}
	for _, path := range scope.Roots {
		lines = append(lines, "- directory: "+formatPath(path))
	}
	for _, path := range scope.Files {
		lines = append(lines, "- file: "+formatPath(path))
	}
	if scope.RequireWrite {
		lines = append(lines, "用户要求创建或修改；只有目标范围内的真实文件变更才能报告完成。")
	}
	volatile := append([]ContextSection(nil), preview.VolatileTail...)
	volatile = append(volatile, ContextSection{Name: "task_scope", Content: strings.Join(lines, "\n")})
	preview.VolatileTail = volatile
	return preview
}

var relativeTurnPathPattern = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_.-]*(?:[/\\][A-Za-z0-9_.-]+)+`)

// inferTurnTaskScope extracts only path-shaped, user-authored target mentions.
// It does not classify the request by product keywords or inject a guessed
// project name. A single-segment name is accepted only when it appears in a
// path-related phrase (or inside a path-like quoted token), which covers new
// directories such as `echocode-agent-web-test` without treating model names or
// ordinary prose as filesystem targets.
func (s *Service) inferTurnTaskScope(prompt string, explicit []string) turnTaskScope {
	workspace := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot)
	if workspace == "" {
		return turnTaskScope{}
	}
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil || workspace == "." || workspace == "" {
		return turnTaskScope{}
	}

	mentions := make([]string, 0, 8)
	addMention := func(candidate string, contextText string) {
		candidate = strings.TrimSpace(strings.Trim(candidate, "`'\"[](){}<>,;:，。；：！？"))
		candidate = strings.TrimSpace(strings.TrimSuffix(candidate, "."))
		if candidate == "" || strings.ContainsAny(candidate, "\r\n\t ") || strings.Contains(candidate, "://") {
			return
		}
		if filepath.IsAbs(filepath.FromSlash(candidate)) || (len(candidate) >= 2 && candidate[1] == ':') {
			return
		}
		cleaned := filepath.Clean(filepath.FromSlash(candidate))
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
			return
		}
		if !strings.ContainsAny(candidate, "/\\") && filepath.Ext(cleaned) == "" && !pathPhraseContext(contextText) {
			return
		}
		if !containsStringValue(mentions, cleaned) {
			mentions = append(mentions, cleaned)
		}
	}

	// Quoted/code-span mentions are the least ambiguous source. Keep the
	// surrounding text because it tells us whether a bare token is a directory
	// or just a model/product name.
	for index := 0; index < len(prompt); {
		quote := prompt[index]
		if quote != '`' && quote != '\'' && quote != '"' {
			index++
			continue
		}
		end := index + 1
		for end < len(prompt) && prompt[end] != quote {
			end++
		}
		if end >= len(prompt) {
			break
		}
		startContext := maxInt(0, index-48)
		endContext := minInt(len(prompt), end+49)
		addMention(prompt[index+1:end], prompt[startContext:endContext])
		index = end + 1
	}

	for _, match := range relativeTurnPathPattern.FindAllStringIndex(prompt, -1) {
		if len(match) != 2 {
			continue
		}
		startContext := maxInt(0, match[0]-24)
		endContext := minInt(len(prompt), match[1]+24)
		addMention(prompt[match[0]:match[1]], prompt[startContext:endContext])
	}

	scope := turnTaskScope{}
	missingTarget := false
	addScopePath := func(abs string) {
		abs, absErr := filepath.Abs(filepath.Clean(abs))
		if absErr != nil {
			return
		}
		if info, statErr := os.Stat(abs); statErr == nil {
			if info.IsDir() {
				scope.Roots = appendUniquePath(scope.Roots, abs)
			} else {
				scope.Files = appendUniquePath(scope.Files, abs)
			}
			return
		}
		if filepath.Ext(abs) != "" {
			scope.Files = appendUniquePath(scope.Files, abs)
		} else {
			scope.Roots = appendUniquePath(scope.Roots, abs)
		}
		missingTarget = true
	}
	// Explicit absolute paths have already passed the temporary access grant
	// validation. Keep them in the task scope even when they are outside the
	// configured workspace (for example, a user-authorized D:\ output folder).
	for _, candidate := range explicit {
		if isTurnAbsolutePath(candidate) {
			addScopePath(candidate)
		}
	}
	isDirectoryMention := func(mention string) bool {
		abs := filepath.Join(workspace, mention)
		if info, statErr := os.Stat(abs); statErr == nil {
			return info.IsDir()
		}
		return filepath.Ext(abs) == ""
	}
	// Add directory targets first. When a request says "create `site` and add
	// `index.html`", the bare filename belongs to the declared directory; it
	// must not become an additional workspace-root write target.
	for _, mention := range mentions {
		if !isDirectoryMention(mention) {
			continue
		}
		abs := filepath.Join(workspace, mention)
		if withinTurnWorkspace(workspace, abs) {
			addScopePath(abs)
		}
	}
	for _, mention := range mentions {
		if isDirectoryMention(mention) {
			continue
		}
		if len(scope.Roots) > 0 && !strings.ContainsAny(mention, `/\`) {
			continue
		}
		abs := filepath.Join(workspace, mention)
		if withinTurnWorkspace(workspace, abs) {
			addScopePath(abs)
		}
	}
	scope.Enabled = len(scope.Roots) > 0 || len(scope.Files) > 0
	scope.RequireWrite = scope.Enabled && (missingTarget || turnPromptHasWriteIntent(prompt))
	return scope
}

func turnPromptHasWriteIntent(prompt string) bool {
	prompt = strings.ToLower(prompt)
	for _, marker := range []string{
		"create", "new", "generate", "write", "save", "modify", "edit", "fix", "build", "implement", "add", "download", "deploy",
		"\u521b\u5efa", "\u65b0\u5efa", "\u751f\u6210", "\u5199\u5165", "\u4fdd\u5b58", "\u4fee\u6539", "\u7f16\u8f91", "\u4fee\u590d", "\u6784\u5efa", "\u5b9e\u73b0", "\u6dfb\u52a0", "\u4e0b\u8f7d", "\u90e8\u7f72",
	} {
		if strings.Contains(prompt, marker) {
			return true
		}
	}
	return false
}

func pathPhraseContext(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{
		"目录", "文件夹", "项目", "路径", "文件", "创建", "新建", "生成", "写入", "保存",
		"directory", "folder", "project", "path", "file", "create", "build", "write", "save",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func isTurnAbsolutePath(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if filepath.IsAbs(candidate) {
		return true
	}
	return len(candidate) >= 3 && candidate[1] == ':' && (candidate[2] == '\\' || candidate[2] == '/')
}

func withinTurnWorkspace(root, target string) bool {
	within, err := pathutil.Within(root, target)
	return err == nil && within
}

func appendUniquePath(values []string, candidate string) []string {
	candidate = filepath.Clean(candidate)
	for _, existing := range values {
		if sameTurnPath(existing, candidate) {
			return values
		}
	}
	return append(values, candidate)
}

func sameTurnPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func containsStringValue(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func mergeTurnRoots(configured, temporary []string) []string {
	merged := make([]string, 0, len(configured)+len(temporary))
	for _, candidate := range append(append([]string(nil), configured...), temporary...) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		duplicate := false
		for _, existing := range merged {
			if strings.EqualFold(existing, candidate) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			merged = append(merged, candidate)
		}
	}
	return merged
}
