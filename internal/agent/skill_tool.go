package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/skills"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// LoadSkillTool lets the model select a relevant Skill from the stable index.
// The host validates identity, enablement, precedence, and context size, but
// does not infer semantic relevance from ordinary user wording.
type LoadSkillTool struct {
	Service *Service
}

func (LoadSkillTool) Name() string { return "load_skill" }

func (LoadSkillTool) Description() string {
	return "按稳定能力索引中的完整名称加载一个已启用 Skill 的正文。由模型根据任务语义决定是否需要；如果当前私有上下文已经包含该 Skill，则不要重复加载。此工具只读取 Skill，不修改工作区。"
}

func (LoadSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "能力索引中显示的完整 Skill 名称。",
			},
		},
		"required": []string{"name"},
	}
}

func (t LoadSkillTool) Execute(_ context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	if t.Service == nil {
		return loadSkillError("Skill 加载器未初始化"), nil
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return loadSkillError("Skill 参数无效: " + err.Error()), nil
	}
	loaded, err := t.Service.loadEnabledAgentSkill(args.Name)
	if err != nil {
		return loadSkillError(err.Error()), nil
	}

	content := strings.Join([]string{
		"[EchoCode loaded skill]",
		"name: " + loaded.Name,
		"version: " + fmt.Sprintf("%d", loaded.Version),
		"sha256: " + loaded.SHA256,
		loaded.Content,
		"[/EchoCode loaded skill]",
	}, "\n")
	content = clipTextToTokenBudget(content, triggeredSkillTokenBudget(t.Service.reasoning))
	if strings.TrimSpace(content) == "" {
		return loadSkillError("Skill 正文为空或超出可用上下文预算"), nil
	}
	summary := fmt.Sprintf("已加载 Skill %s（version %d，%s）", loaded.Name, loaded.Version, loaded.SHA256)
	return tools.Result{
		Summary: content,
		Parts: []tools.ResultPart{{
			Kind: tools.PartToolCall, Name: "load_skill", Status: "ok", Input: loaded.Name, Output: summary,
		}},
	}, nil
}

func (s *Service) loadEnabledAgentSkill(name string) (skills.LoadedSkill, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 128 {
		return skills.LoadedSkill{}, fmt.Errorf("Skill 名称无效")
	}

	found := false
	for _, entry := range s.loadSkillsIndex() {
		if entry.Name != name {
			continue
		}
		if entry.Disabled {
			return skills.LoadedSkill{}, fmt.Errorf("Skill 已禁用：%s", name)
		}
		found = true
		break
	}
	if !found {
		return skills.LoadedSkill{}, fmt.Errorf("能力索引中不存在 Skill：%s", name)
	}

	loaders := s.skillLoaders()
	for index := len(loaders) - 1; index >= 0; index-- {
		loaded, err := loaders[index].Load(name)
		if err == nil {
			return loaded, nil
		}
	}
	return skills.LoadedSkill{}, fmt.Errorf("无法读取 Skill：%s", name)
}

func loadSkillError(message string) tools.Result {
	return tools.Result{Summary: message, IsError: true}
}
