package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// BrowserController is implemented by the desktop browser bridge. It keeps
// the tool package independent from the concrete CDP engine.
type BrowserController interface {
	OpenURL(context.Context, string) (string, error)
	SnapshotJSON(context.Context) (string, error)
	ClickSelector(context.Context, string) error
	TypeSelector(context.Context, string, string) error
	PressKey(context.Context, string) error
	Back(context.Context) error
	Forward(context.Context) error
	Reload(context.Context) error
	Scroll(context.Context, float64, float64) error
	CloseTab(context.Context) error
	SaveScreenshot(context.Context, string) (string, error)
}

type BrowserTool struct {
	Policy     SandboxPolicy
	Controller BrowserController
}

func (t BrowserTool) Name() string { return "browser" }

func (t BrowserTool) Description() string {
	return "控制 EchoCode 内置浏览器。仅在网页导航、读取或交互确实有助于当前任务时使用；打开外部网站仍受用户审批策略控制。普通网络检索优先使用 web_search，并在回答中列出实际使用的来源链接。可读取页面快照、点击、输入、按键和保存截图。不要使用 run_command 启动浏览器。"
}

func (t BrowserTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"open", "snapshot", "click", "type", "key", "back", "forward", "reload", "scroll", "close_tab", "screenshot"},
				"description": "要执行的浏览器操作",
			},
			"url":      map[string]any{"type": "string", "description": "open 操作的 HTTP/HTTPS 地址"},
			"selector": map[string]any{"type": "string", "description": "snapshot 返回的 CSS selector"},
			"text":     map[string]any{"type": "string", "description": "type 操作输入的文本"},
			"key":      map[string]any{"type": "string", "description": "key 操作的按键，例如 Enter、Tab、Escape"},
			"delta_x":  map[string]any{"type": "number"},
			"delta_y":  map[string]any{"type": "number"},
			"path":     map[string]any{"type": "string", "description": "截图保存到工作区内的相对路径"},
		},
		"required": []string{"action"},
	}
}

func (t BrowserTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return errorResult("内置浏览器控制器不可用"), nil
	}
	var args struct {
		Action   string  `json:"action"`
		URL      string  `json:"url"`
		Selector string  `json:"selector"`
		Text     string  `json:"text"`
		Key      string  `json:"key"`
		Path     string  `json:"path"`
		DeltaX   float64 `json:"delta_x"`
		DeltaY   float64 `json:"delta_y"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	input := action
	output := ""
	summary := ""
	var err error
	switch action {
	case "open":
		if strings.TrimSpace(args.URL) == "" {
			return errorResult("open 操作需要 url"), nil
		}
		input = args.URL
		output, err = t.Controller.OpenURL(ctx, args.URL)
		summary = "已在 EchoCode 内置浏览器中打开 " + args.URL
	case "snapshot":
		output, err = t.Controller.SnapshotJSON(ctx)
		summary = "已读取当前网页的文本与可交互元素"
	case "click":
		if strings.TrimSpace(args.Selector) == "" {
			return errorResult("click 操作需要 selector"), nil
		}
		input = args.Selector
		err = t.Controller.ClickSelector(ctx, args.Selector)
		output = "点击完成"
		summary = "已点击页面元素 " + args.Selector
	case "type":
		if strings.TrimSpace(args.Selector) == "" {
			return errorResult("type 操作需要 selector"), nil
		}
		input = args.Selector
		err = t.Controller.TypeSelector(ctx, args.Selector, args.Text)
		output = "输入完成"
		summary = "已向页面元素输入文本"
	case "key":
		if strings.TrimSpace(args.Key) == "" {
			return errorResult("key 操作需要 key"), nil
		}
		input = args.Key
		err = t.Controller.PressKey(ctx, args.Key)
		output = "按键完成"
		summary = "已向当前网页发送按键 " + args.Key
	case "back":
		err = t.Controller.Back(ctx)
		output, summary = "后退完成", "已后退当前网页"
	case "forward":
		err = t.Controller.Forward(ctx)
		output, summary = "前进完成", "已前进当前网页"
	case "reload":
		err = t.Controller.Reload(ctx)
		output, summary = "刷新完成", "已刷新当前网页"
	case "scroll":
		err = t.Controller.Scroll(ctx, args.DeltaX, args.DeltaY)
		output, summary = "滚动完成", "已滚动当前网页"
	case "close_tab":
		err = t.Controller.CloseTab(ctx)
		output, summary = "标签页已关闭", "已关闭当前浏览器标签页"
	case "screenshot":
		path := strings.TrimSpace(args.Path)
		if path == "" {
			path = "echocode-browser-screenshot.png"
		}
		resolved, resolveErr := t.Policy.ResolveWritePath(path)
		if resolveErr != nil {
			return errorResult(resolveErr.Error()), nil
		}
		output, err = t.Controller.SaveScreenshot(ctx, resolved)
		if err == nil {
			attachment, attachmentErr := AttachmentFromFile(resolved)
			attachments := []Attachment(nil)
			if attachmentErr == nil {
				attachments = []Attachment{attachment}
			}
			return Result{
				Summary:     "浏览器截图已保存到 " + path,
				Attachments: attachments,
				Parts: []ResultPart{
					{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: path, Output: output},
					{Kind: PartFile, Path: path, FileAction: "created", Created: true},
				},
			}, nil
		}
	default:
		return errorResult(fmt.Sprintf("不支持的 browser action: %s", action)), nil
	}
	if err != nil {
		message := fmt.Sprintf("browser %s 执行失败: %v", action, err)
		if strings.Contains(err.Error(), "浏览标签页不存在") {
			message += "；请先执行 browser open 打开目标网页"
		}
		return Result{
			Summary: message,
			IsError: true,
			Parts: []ResultPart{{
				Kind: PartToolCall, Name: t.Name(), Status: "error", Input: input, Output: message,
			}},
		}, nil
	}
	return Result{
		Summary: summary,
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: input, Output: output,
		}},
	}, nil
}
