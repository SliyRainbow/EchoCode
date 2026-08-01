package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
)

type ComputerController interface {
	ListWindows(context.Context) ([]computercontrol.Window, error)
	FocusWindow(context.Context, string) error
	ClickWindow(context.Context, string, int, int) error
	TypeText(context.Context, string, string) error
	PressKey(context.Context, string, string, bool, bool, bool) error
	ScreenshotWindow(context.Context, string, string) (string, error)
}

// ComputerTool controls explicitly allowed top-level desktop windows.
type ComputerTool struct {
	Policy     SandboxPolicy
	Controller ComputerController
}

func (t ComputerTool) Name() string { return "computer" }

func (t ComputerTool) Description() string {
	return "控制用户明确允许的 Windows 应用窗口。先 list_windows 获取窗口 ID，再 screenshot 检查画面；click 坐标相对窗口左上角。支持 focus、click、type、key 和 screenshot。"
}

func (t ComputerTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string", "enum": []string{"list_windows", "focus", "screenshot", "click", "type", "key"},
			},
			"window_id": map[string]any{"type": "string", "description": "list_windows 返回的窗口 ID"},
			"x":         map[string]any{"type": "integer", "description": "相对窗口左边的横坐标"},
			"y":         map[string]any{"type": "integer", "description": "相对窗口顶部的纵坐标"},
			"text":      map[string]any{"type": "string", "description": "type 操作输入的文本"},
			"key":       map[string]any{"type": "string", "description": "Enter、Tab、Escape、方向键、字母、数字或 F1-F12"},
			"ctrl":      map[string]any{"type": "boolean"},
			"alt":       map[string]any{"type": "boolean"},
			"shift":     map[string]any{"type": "boolean"},
			"path":      map[string]any{"type": "string", "description": "截图保存到工作区的相对路径"},
		},
		"required": []string{"action"},
	}
}

func (t ComputerTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return computerError("computer 控制器不可用", ""), nil
	}
	var args struct {
		Action   string `json:"action"`
		WindowID string `json:"window_id"`
		X        int    `json:"x"`
		Y        int    `json:"y"`
		Text     string `json:"text"`
		Key      string `json:"key"`
		Ctrl     bool   `json:"ctrl"`
		Alt      bool   `json:"alt"`
		Shift    bool   `json:"shift"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return computerError("参数解析失败: "+err.Error(), ""), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	input := strings.TrimSpace(action + " " + args.WindowID)
	summary := ""
	output := ""
	var err error

	if action == "list_windows" {
		var windowsList []computercontrol.Window
		windowsList, err = t.Controller.ListWindows(ctx)
		if err == nil {
			encoded, encodeErr := json.MarshalIndent(windowsList, "", "  ")
			if encodeErr != nil {
				err = encodeErr
			} else {
				output = string(encoded)
				summary = fmt.Sprintf("找到 %d 个允许操控的窗口", len(windowsList))
			}
		}
	} else {
		if strings.TrimSpace(args.WindowID) == "" {
			return computerError(action+" 操作需要 window_id", input), nil
		}
		switch action {
		case "focus":
			err = t.Controller.FocusWindow(ctx, args.WindowID)
			summary = "已聚焦目标窗口"
		case "click":
			err = t.Controller.ClickWindow(ctx, args.WindowID, args.X, args.Y)
			summary = fmt.Sprintf("已点击目标窗口坐标 %d,%d", args.X, args.Y)
			input = fmt.Sprintf("click %s @ %d,%d", args.WindowID, args.X, args.Y)
		case "type":
			if args.Text == "" {
				return computerError("type 操作需要 text", input), nil
			}
			err = t.Controller.TypeText(ctx, args.WindowID, args.Text)
			summary = fmt.Sprintf("已向目标窗口输入 %d 个字符", len([]rune(args.Text)))
		case "key":
			if strings.TrimSpace(args.Key) == "" {
				return computerError("key 操作需要 key", input), nil
			}
			err = t.Controller.PressKey(ctx, args.WindowID, args.Key, args.Ctrl, args.Alt, args.Shift)
			summary = "已向目标窗口发送按键 " + args.Key
			input = strings.TrimSpace(fmt.Sprintf("key %s %s", args.WindowID, args.Key))
		case "screenshot":
			path := strings.TrimSpace(args.Path)
			if path == "" {
				path = "echocode-window-screenshot.png"
			}
			resolved, resolveErr := t.Policy.ResolveWritePath(path)
			if resolveErr != nil {
				return computerError(resolveErr.Error(), input), nil
			}
			output, err = t.Controller.ScreenshotWindow(ctx, args.WindowID, resolved)
			if err == nil {
				attachment, attachmentErr := AttachmentFromFile(resolved)
				attachments := []Attachment(nil)
				if attachmentErr == nil {
					attachments = []Attachment{attachment}
				}
				return Result{
					Summary:     "窗口截图已保存到 " + path,
					Attachments: attachments,
					Parts: []ResultPart{
						{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: input, Output: output},
						{Kind: PartFile, Path: path, FileAction: "created", Created: true},
					},
				}, nil
			}
		default:
			return computerError("不支持的 computer action: "+action, input), nil
		}
	}
	if err != nil {
		return computerError(fmt.Sprintf("computer %s 执行失败: %v", action, err), input), nil
	}
	return Result{
		Summary: summary,
		Parts:   []ResultPart{{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: input, Output: output}},
	}, nil
}

func computerError(message, input string) Result {
	return Result{
		Summary: message,
		IsError: true,
		Parts:   []ResultPart{{Kind: PartToolCall, Name: "computer", Status: "error", Input: input, Output: message}},
	}
}
