package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const maxPlanSteps = 12

// UpdatePlanTool validates a complete checklist and lets the host persist it.
type UpdatePlanTool struct {
	OnUpdate func([]ProgressStep) error
}

func (UpdatePlanTool) Name() string { return "update_plan" }

func (UpdatePlanTool) Description() string {
	return "更新当前多步骤任务的执行清单。仅用于需要多步操作的任务；简单问答不要调用。每次传入完整清单，且最多一个步骤为 in_progress。"
}

func (UpdatePlanTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maxPlanSteps,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": map[string]any{"type": "string", "description": "简短、可验证的步骤名称"},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required":             []string{"title", "status"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"steps"},
		"additionalProperties": false,
	}
}

func (t UpdatePlanTool) Execute(_ context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Steps []ProgressStep `json:"steps"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("计划参数解析失败: " + err.Error()), nil
	}
	if len(args.Steps) == 0 || len(args.Steps) > maxPlanSteps {
		return errorResult(fmt.Sprintf("计划步骤数必须在 1-%d 之间", maxPlanSteps)), nil
	}

	inProgress := 0
	completed := 0
	for index := range args.Steps {
		args.Steps[index].Title = strings.TrimSpace(args.Steps[index].Title)
		if args.Steps[index].Title == "" {
			return errorResult(fmt.Sprintf("第 %d 个计划步骤标题不能为空", index+1)), nil
		}
		if len([]rune(args.Steps[index].Title)) > 160 {
			return errorResult(fmt.Sprintf("第 %d 个计划步骤标题过长", index+1)), nil
		}
		switch args.Steps[index].Status {
		case "pending":
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		default:
			return errorResult(fmt.Sprintf("第 %d 个计划步骤状态无效", index+1)), nil
		}
	}
	if inProgress > 1 {
		return errorResult("计划中最多只能有一个进行中的步骤"), nil
	}
	if t.OnUpdate != nil {
		steps := append([]ProgressStep(nil), args.Steps...)
		if err := t.OnUpdate(steps); err != nil {
			return errorResult("计划状态保存失败: " + err.Error()), nil
		}
	}
	taskStatus := "running"
	if completed == len(args.Steps) {
		taskStatus = "completed"
	}

	return Result{
		Summary: fmt.Sprintf("任务进度已更新：%d/%d 个步骤完成", completed, len(args.Steps)),
		Parts: []ResultPart{{
			Kind:       PartProgress,
			Steps:      args.Steps,
			TaskStatus: taskStatus,
		}},
	}, nil
}

// SummarizeFileChanges reports the final task diff rather than summing repeated
// edits to the same file.
func SummarizeFileChanges(changes []FileChange) (files, additions, deletions int) {
	type aggregate struct {
		path   string
		before string
		after  string
	}
	byPath := make(map[string]*aggregate)
	order := make([]string, 0, len(changes))
	for _, change := range changes {
		key := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(change.Path), "\\", "/"))
		if key == "" {
			continue
		}
		item, ok := byPath[key]
		if !ok {
			item = &aggregate{path: change.Path, before: change.Before}
			byPath[key] = item
			order = append(order, key)
		}
		item.after = change.After
	}
	for _, key := range order {
		item := byPath[key]
		if item.before == item.after {
			continue
		}
		_, adds, dels := unifiedDiff(item.path, item.before, item.after)
		files++
		additions += adds
		deletions += dels
	}
	return files, additions, deletions
}
