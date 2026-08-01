package agent

import "fmt"

type ReasoningLevel string

const (
	ReasoningNone   ReasoningLevel = "none"
	ReasoningLow    ReasoningLevel = "low"
	ReasoningMedium ReasoningLevel = "medium"
	ReasoningHigh   ReasoningLevel = "high"
	ReasoningXHigh  ReasoningLevel = "xhigh"
	ReasoningMax    ReasoningLevel = "max"

	// ReasoningUltra remains as a source-compatible alias for older callers.
	// The old product label was never a valid upstream wire value.
	ReasoningUltra = ReasoningMax
)

const DefaultReasoningLevel = ReasoningMax

type ReasoningBudget struct {
	ContextPolicy string `json:"contextPolicy"`
	CachePolicy   string `json:"cachePolicy"`
	Planner       bool   `json:"planner"`
}

type ReasoningProfile struct {
	ID          ReasoningLevel  `json:"id"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Budget      ReasoningBudget `json:"budget"`
}

var reasoningProfiles = []ReasoningProfile{
	{
		ID:          ReasoningNone,
		Label:       "关闭",
		Description: "不请求模型进行额外推理",
		Budget: ReasoningBudget{
			ContextPolicy: "minimal",
			CachePolicy:   "reuse-prefix",
			Planner:       false,
		},
	},
	{
		ID:          ReasoningLow,
		Label:       "轻度",
		Description: "简单问答、轻量编辑、低成本优先",
		Budget: ReasoningBudget{
			ContextPolicy: "minimal",
			CachePolicy:   "reuse-prefix",
			Planner:       false,
		},
	},
	{
		ID:          ReasoningMedium,
		Label:       "中",
		Description: "普通代码修改、单文件任务",
		Budget: ReasoningBudget{
			ContextPolicy: "task-summary",
			CachePolicy:   "reuse-prefix",
			Planner:       false,
		},
	},
	{
		ID:          ReasoningHigh,
		Label:       "高",
		Description: "跨文件修改、复杂 bug、测试修复",
		Budget: ReasoningBudget{
			ContextPolicy: "expanded",
			CachePolicy:   "stable-prefix",
			Planner:       true,
		},
	},
	{
		ID:          ReasoningXHigh,
		Label:       "很高",
		Description: "大型实现、深入排查、多阶段验证",
		Budget: ReasoningBudget{
			ContextPolicy: "full-relevant",
			CachePolicy:   "strict-stable-prefix",
			Planner:       true,
		},
	},
	{
		ID:          ReasoningMax,
		Label:       "极高",
		Description: "协议设计、Agent 架构、发布级检查",
		Budget: ReasoningBudget{
			ContextPolicy: "full-relevant",
			CachePolicy:   "strict-stable-prefix",
			Planner:       true,
		},
	},
}

func ReasoningProfiles() []ReasoningProfile {
	profiles := make([]ReasoningProfile, len(reasoningProfiles))
	copy(profiles, reasoningProfiles)
	return profiles
}

func ParseReasoningLevel(value string) (ReasoningLevel, error) {
	if value == "ultra" {
		value = string(ReasoningMax)
	}
	level := ReasoningLevel(value)
	profile, ok := ReasoningProfileFor(level)
	if !ok {
		return "", fmt.Errorf("unknown reasoning level %q", value)
	}
	return profile.ID, nil
}

func ReasoningProfileFor(level ReasoningLevel) (ReasoningProfile, bool) {
	if level == "ultra" {
		level = ReasoningMax
	}
	for _, profile := range reasoningProfiles {
		if profile.ID == level {
			return profile, true
		}
	}
	return ReasoningProfile{}, false
}
