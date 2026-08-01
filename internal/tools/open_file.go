package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenFileTool opens a validated workspace file through the desktop host. It
// deliberately bypasses the shell so platform-specific quoting cannot corrupt
// the path and the model cannot use opening as an escape from workspace policy.
type OpenFileTool struct {
	Policy  SandboxPolicy
	Open    func(string) error
	Preview func(string) error
}

func (t OpenFileTool) Name() string { return "open_file" }

func (t OpenFileTool) Description() string {
	return "打开当前工作区内的文件。HTML 优先使用 EchoCode 内置浏览器预览，其他文件使用系统默认应用；禁止改用 run_command。"
}

func (t OpenFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "相对于当前工作区根目录的文件路径"},
		},
		"required": []string{"path"},
	}
}

func (t OpenFileTool) Execute(_ context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return errorResult("path 不能为空"), nil
	}
	abs, err := t.Policy.ResolveReadPath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return errorResult("文件不存在或无法访问: " + err.Error()), nil
	}
	if info.IsDir() {
		return errorResult("目标是目录，无法作为文件打开"), nil
	}
	usePreview := isHTMLPath(abs) && t.Preview != nil
	if !usePreview && t.Open == nil {
		return errorResult("当前桌面环境不支持打开文件"), nil
	}
	if usePreview {
		if err := t.Preview(abs); err != nil {
			return errorResult("内置浏览器预览失败: " + err.Error()), nil
		}
	} else if err := t.Open(abs); err != nil {
		return errorResult("系统打开文件失败: " + err.Error()), nil
	}
	summary := fmt.Sprintf("已使用系统默认应用打开 %s", args.Path)
	output := "系统已接收打开请求"
	if usePreview {
		summary = fmt.Sprintf("已在 EchoCode 内置浏览器中预览 %s", args.Path)
		output = "EchoCode 内置浏览器已打开预览"
	}
	return Result{
		Summary: summary,
		Parts: []ResultPart{
			{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Path, Output: output},
			{Kind: PartFile, Path: args.Path, FileAction: "available"},
		},
	}, nil
}

func isHTMLPath(filePath string) bool {
	extension := strings.ToLower(filepath.Ext(filePath))
	return extension == ".html" || extension == ".htm"
}
