package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

func TestLoadSkillToolLetsModelSelectLegacySkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "legacy", "legacy-project-helper", "为复杂项目选择专用构建流程")
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()

	index := service.loadSkillsIndex()
	if loaded := service.loadTriggeredSkills("为复杂项目选择专用构建流程", index); len(loaded) != 0 {
		t.Fatalf("legacy description became host intent routing: %#v", loaded)
	}

	result, err := (LoadSkillTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"name":"legacy-project-helper"}`))
	if err != nil || result.IsError {
		t.Fatalf("load skill result=%#v err=%v", result, err)
	}
	if !strings.Contains(result.Summary, "[EchoCode loaded skill]") || !strings.Contains(result.Summary, "正文") {
		t.Fatalf("loaded skill content = %q", result.Summary)
	}
	if len(result.Parts) != 1 || strings.Contains(result.Parts[0].Output, "正文") {
		t.Fatalf("skill UI output should stay compact: %#v", result.Parts)
	}
}

func TestLoadSkillToolRejectsDisabledSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "disabled", "disabled-helper", "disabled helper")
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()
	service.runtimeSettings.Skills.Disabled = []string{"disabled-helper"}

	result, err := (LoadSkillTool{Service: service}).Execute(context.Background(), json.RawMessage(`{"name":"disabled-helper"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Summary, "已禁用") {
		t.Fatalf("disabled skill result = %#v", result)
	}
}

func TestToolLoopFeedsModelSelectedSkillInstructionsBackToModel(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "selected", "selected-helper", "model selected helper")
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()
	registry := service.buildReadOnlyRegistry()
	if _, ok := registry.Get("load_skill"); !ok {
		t.Fatal("read-only registry omitted load_skill")
	}

	completionCalls := 0
	outcome, err := service.runToolLoopWithCompletion(
		context.Background(),
		registry,
		protocol.ChatRequest{Messages: []protocol.Message{{Role: "user", Content: "use the indexed capability"}}},
		func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
			completionCalls++
			if completionCalls == 1 {
				return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{
					ID: "load-selected", Type: "function", Function: protocol.ToolCallFunction{
						Name: "load_skill", Arguments: json.RawMessage(`{"name":"selected-helper"}`),
					},
				}}}, nil
			}
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, "正文") {
				t.Fatalf("model did not receive loaded skill: %#v", last)
			}
			return protocol.CompletionResult{Content: "已按所选 Skill 完成。"}, nil
		},
		nil,
	)
	if err != nil || outcome.Content != "已按所选 Skill 完成。" || completionCalls != 2 {
		t.Fatalf("outcome=%#v calls=%d err=%v", outcome, completionCalls, err)
	}
}
