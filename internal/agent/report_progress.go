package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const maxReportedProgressRunes = 360

// ReportProgressTool gives the model a narrow, user-visible status channel.
// It is deliberately limited to observable milestones rather than private
// reasoning, so the timeline remains useful without exposing chain of thought.
type ReportProgressTool struct{}

func (ReportProgressTool) Name() string { return "report_progress" }

func (ReportProgressTool) Description() string {
	return "在任务执行期间向用户时间线写入一条简洁、可核验的进展。仅用于已确认的事实、当前验证、等待或改变失败策略；不要写私有推理、逐步思考、未验证猜测或敏感信息。"
}

func (ReportProgressTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "用户可见的可核验进展，最多 360 个字符。",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"running", "waiting", "retrying"},
				"description": "当前状态；默认 running。",
			},
		},
		"required": []string{"message"},
	}
}

func (ReportProgressTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	var args struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return reportedProgressError("进展参数解析失败: " + err.Error()), nil
	}
	message := clipContextText(redactSensitiveText(strings.TrimSpace(args.Message)), maxReportedProgressRunes)
	if message == "" {
		return reportedProgressError("进展内容不能为空"), nil
	}
	status := strings.TrimSpace(args.Status)
	if status == "" {
		status = "running"
	}
	switch status {
	case "running", "waiting", "retrying":
	default:
		return reportedProgressError(fmt.Sprintf("进展状态无效: %s", status)), nil
	}
	part := tools.ResultPart{Kind: tools.PartTimelineNote, Message: message, Status: status}
	tools.EmitProgress(ctx, part)
	return tools.Result{
		Summary: "已记录本轮工作进展。",
		Parts:   []tools.ResultPart{part},
	}, nil
}

func reportedProgressError(summary string) tools.Result {
	return tools.Result{Summary: summary, IsError: true}
}
