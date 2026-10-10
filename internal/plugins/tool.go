package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type pluginTool struct {
	manager    *Manager
	record     record
	setting    Setting
	descriptor ToolManifest
	policy     tools.SandboxPolicy
	limits     runnerLimits
}

func (t *pluginTool) Name() string {
	return namespacedToolName(t.record.manifest.ID, t.descriptor.Name)
}

func (t *pluginTool) Description() string {
	return fmt.Sprintf("[%s] %s", t.record.manifest.Name, t.descriptor.Description)
}

func (t *pluginTool) InputSchema() map[string]any { return t.descriptor.InputSchema }

func (t *pluginTool) ReadOnly() bool { return t.descriptor.ReadOnly }

func (t *pluginTool) Execute(ctx context.Context, rawArgs json.RawMessage) (result tools.Result, err error) {
	if !grantContains(t.setting.Permissions, t.descriptor.Permissions) {
		return pluginErrorResult(t.Name(), "插件缺少该工具所需的用户授权"), nil
	}
	if t.descriptor.Permissions.Network {
		if err := t.policy.CheckNetwork(); err != nil {
			return pluginErrorResult(t.Name(), err.Error()), nil
		}
	}
	arguments := map[string]any{}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &arguments); err != nil {
			return pluginErrorResult(t.Name(), "插件工具参数不是有效 JSON 对象: "+err.Error()), nil
		}
	}
	if err := t.resolvePaths(arguments); err != nil {
		return pluginErrorResult(t.Name(), err.Error()), nil
	}
	outputStates := t.captureOutputFileStates(arguments)
	before, err := t.captureOutputSnapshots(arguments)
	if err != nil {
		return tools.Result{}, err
	}
	defer func() {
		changes, snapshotErr := t.outputSnapshotChanges(before)
		if snapshotErr != nil {
			var restoreErr error
			for _, original := range before {
				restoreErr = errors.Join(restoreErr, tools.RestoreFile(t.policy, original.path, original.content, original.existed, "", string(tools.EncodingBinary), false))
			}
			err = errors.Join(err, fmt.Errorf("保存插件文件快照失败: %w", snapshotErr), restoreErr)
			return
		}
		result.Changes = append(result.Changes, changes...)
	}()
	timeout := t.descriptor.TimeoutSeconds
	if timeout <= 0 || timeout > t.limits.maxExecutionSeconds {
		timeout = t.limits.maxExecutionSeconds
	}
	if timeout < 5 {
		timeout = 5
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	startedAt := time.Now()
	tools.EmitProgress(ctx, tools.ResultPart{
		Kind: tools.PartToolCall, Name: t.Name(), Status: "waiting", Input: boundedText(string(rawArgs), 4000),
		Output: "正在等待插件运行时返回结果", StartedAt: startedAt.Format(time.RFC3339Nano),
	})
	if t.record.source == "builtin" {
		result, err = runBuiltinWorker(callCtx, t.manager.AppVersion(), t.record, t.descriptor, arguments, t.policy, t.setting.Permissions, t.limits)
	} else {
		result, err = runExternal(callCtx, t.manager.AppVersion(), t.record, t.descriptor, arguments, t.policy, t.setting.Permissions, t.limits)
	}
	completedAt := time.Now()
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("插件工具超过 %d 秒执行上限", timeout)
		} else if errors.Is(callCtx.Err(), context.Canceled) {
			err = context.Canceled
		}
		return tools.Result{}, err
	}
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = "插件工具执行完成"
	}
	fileParts := t.outputFileParts(arguments, outputStates)
	if !result.IsError && len(fileParts) > 0 && t.record.manifest.ID == ArtifactPluginID {
		tools.EmitProgress(ctx, tools.ResultPart{
			Kind: tools.PartToolCall, Name: t.Name(), Status: "running", Input: boundedText(string(rawArgs), 4000), Output: "正在重新读取并验证生成的办公产物",
		})
		verification, verifyErr := t.verifyOutputArtifacts(arguments)
		if verifyErr != nil {
			result.IsError = true
			result.Summary = strings.TrimSpace(result.Summary + "\n产物验证失败：" + verifyErr.Error())
		} else if verification != "" {
			result.Summary = strings.TrimSpace(result.Summary + "\n\n产物验证通过：\n" + verification)
		}
	}
	result.Parts = append(result.Parts, tools.ResultPart{
		Kind:        tools.PartToolCall,
		Name:        t.Name(),
		Status:      map[bool]string{true: "error", false: "ok"}[result.IsError],
		Input:       boundedText(string(rawArgs), 4000),
		Output:      boundedText(result.Summary, 16*1024),
		StartedAt:   startedAt.Format(time.RFC3339Nano),
		CompletedAt: completedAt.Format(time.RFC3339Nano),
		DurationMs:  completedAt.Sub(startedAt).Milliseconds(),
	})
	result.Parts = append(result.Parts, fileParts...)
	return result, nil
}

func (t *pluginTool) captureOutputFileStates(arguments map[string]any) map[string]bool {
	states := make(map[string]bool)
	for _, requirement := range t.descriptor.Paths {
		if requirement.Access != "write" {
			continue
		}
		path, ok := arguments[requirement.Argument].(string)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		resolved, err := t.policy.ResolveReadPath(path)
		if err != nil {
			continue
		}
		info, statErr := os.Stat(resolved)
		states[strings.ToLower(filepath.Clean(resolved))] = statErr == nil && !info.IsDir()
	}
	return states
}

func (t *pluginTool) outputFileParts(arguments map[string]any, before map[string]bool) []tools.ResultPart {
	seen := make(map[string]bool)
	parts := make([]tools.ResultPart, 0)
	for _, requirement := range t.descriptor.Paths {
		if requirement.Access != "write" {
			continue
		}
		path, ok := arguments[requirement.Argument].(string)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		resolved, err := t.policy.ResolveReadPath(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}
		displayPath := pluginDisplayPath(t.policy.WorkspaceRoot, resolved)
		key := strings.ToLower(filepath.Clean(resolved))
		if seen[key] {
			continue
		}
		seen[key] = true
		fileAction := "available"
		created := false
		if existed, tracked := before[key]; tracked {
			created = !existed
			if created {
				fileAction = "created"
			} else {
				fileAction = "modified"
			}
		}
		parts = append(parts, tools.ResultPart{Kind: tools.PartFile, Path: displayPath, FileAction: fileAction, Created: created})
	}
	return parts
}

func (t *pluginTool) verifyOutputArtifacts(arguments map[string]any) (string, error) {
	verified := make([]string, 0)
	seen := make(map[string]bool)
	for _, requirement := range t.descriptor.Paths {
		if requirement.Access != "write" {
			continue
		}
		path, ok := arguments[requirement.Argument].(string)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		resolved, err := t.policy.ResolveReadPath(path)
		if err != nil {
			return "", err
		}
		key := strings.ToLower(filepath.Clean(resolved))
		if seen[key] {
			continue
		}
		seen[key] = true
		kind, _, supported := artifacts.Detect(resolved)
		if !supported {
			continue
		}
		summary, verifyErr := artifactVerificationSummary(resolved, kind)
		if verifyErr != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(resolved), verifyErr)
		}
		verified = append(verified, summary)
	}
	return strings.Join(verified, "\n"), nil
}

func artifactVerificationSummary(path string, kind artifacts.Kind) (string, error) {
	if kind == artifacts.KindSpreadsheet {
		summary, err := artifacts.SpreadsheetSummary(path)
		if err != nil {
			return "", err
		}
		return summary, nil
	}
	preview, err := artifacts.PreviewFile(path, artifacts.DefaultPreviewOptions())
	if err != nil {
		return "", err
	}
	switch kind {
	case artifacts.KindDocument:
		blocks := 0
		if preview.Document != nil {
			blocks = len(preview.Document.Blocks)
		}
		return fmt.Sprintf("%s：DOCX 可重新打开，读取到 %d 个内容块", filepath.Base(path), blocks), nil
	case artifacts.KindPresentation:
		slides := 0
		if preview.Presentation != nil {
			slides = len(preview.Presentation.Slides)
		}
		return fmt.Sprintf("%s：PPTX 可重新打开，读取到 %d 页幻灯片", filepath.Base(path), slides), nil
	default:
		return "", fmt.Errorf("不支持验证的办公产物类型 %q", kind)
	}
}

func pluginDisplayPath(workspaceRoot, path string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root != "" {
		if relative, err := filepath.Rel(root, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.Clean(path)
}

func (t *pluginTool) resolvePaths(arguments map[string]any) error {
	for _, requirement := range t.descriptor.Paths {
		value, ok := arguments[requirement.Argument]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			if requirement.Optional {
				continue
			}
			return fmt.Errorf("插件工具缺少路径参数 %q", requirement.Argument)
		}
		path, ok := value.(string)
		if !ok {
			return fmt.Errorf("插件路径参数 %q 必须是字符串", requirement.Argument)
		}
		var (
			resolved string
			err      error
		)
		if requirement.Access == "write" {
			resolved, err = t.policy.ResolveWritePath(path)
		} else {
			resolved, err = t.policy.ResolveReadPath(path)
		}
		if err != nil {
			return fmt.Errorf("插件路径参数 %q: %w", requirement.Argument, err)
		}
		arguments[requirement.Argument] = resolved
	}
	return nil
}

func pluginErrorResult(name, message string) tools.Result {
	return tools.Result{
		Summary: message,
		IsError: true,
		Parts:   []tools.ResultPart{{Kind: tools.PartToolCall, Name: name, Status: "error", Output: message}},
	}
}

func boundedText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("\n... [truncated %d bytes]", len(value)-limit)
}
