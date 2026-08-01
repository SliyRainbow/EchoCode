package cache

import "math"

const (
	TargetHitRate            = 0.96
	nearTargetHitRate        = 0.95
	shortPromptTokenLimit    = 1000
	tinyPromptTokenLimit     = 64
	residualMissTokenLimit   = 256
	warmupSampleObserveLimit = 3
)

type UsageMetrics struct {
	PromptCacheHitTokens  int64   `json:"promptCacheHitTokens"`
	PromptCacheMissTokens int64   `json:"promptCacheMissTokens"`
	InputTokens           int64   `json:"inputTokens"`
	OutputTokens          int64   `json:"outputTokens"`
	EffectiveCost         float64 `json:"effectiveCost"`
}

type Health struct {
	Status                    string  `json:"status"`
	Message                   string  `json:"message"`
	HitRate                   float64 `json:"hitRate"`
	TargetHitRate             float64 `json:"targetHitRate"`
	HitTokens                 int64   `json:"hitTokens"`
	MissTokens                int64   `json:"missTokens"`
	TotalCacheTokens          int64   `json:"totalCacheTokens"`
	MissTokenBudget           int64   `json:"missTokenBudget"`
	RequiredHitTokens         int64   `json:"requiredHitTokens"`
	AdditionalHitTokensNeeded int64   `json:"additionalHitTokensNeeded"`
	ShortPrompt               bool    `json:"shortPrompt"`
	SampleCount               int     `json:"sampleCount"`
	ConsecutiveBelowTarget    int     `json:"consecutiveBelowTarget"`
	HitTokensIncreasing       bool    `json:"hitTokensIncreasing"`
	MissTokensStable          bool    `json:"missTokensStable"`
	MissTokensImproving       bool    `json:"missTokensImproving"`
}

func (m UsageMetrics) CacheHitRate() float64 {
	total := m.PromptCacheHitTokens + m.PromptCacheMissTokens
	if total == 0 {
		return 0
	}
	return float64(m.PromptCacheHitTokens) / float64(total)
}

func (m UsageMetrics) HasCacheTokens() bool {
	return m.PromptCacheHitTokens+m.PromptCacheMissTokens > 0
}

func (m UsageMetrics) BelowTarget() bool {
	return m.HasCacheTokens() && m.CacheHitRate() < TargetHitRate
}

func Analyze(m UsageMetrics) Health {
	return AnalyzeHistory([]UsageMetrics{m})
}

func AnalyzeHistory(history []UsageMetrics) Health {
	if len(history) == 0 {
		return AnalyzeSingle(UsageMetrics{})
	}
	health := AnalyzeSingle(history[len(history)-1])
	health.SampleCount = countSamples(history)
	health.ConsecutiveBelowTarget = consecutiveBelowTarget(history)
	health.HitTokensIncreasing = hitTokensIncreasing(history)
	health.MissTokensStable = missTokensStable(history)
	health.MissTokensImproving = missTokensImproving(history)

	if health.Status == "warming" && health.SampleCount <= warmupSampleObserveLimit {
		health.Message = "DeepSeek 官方上下文缓存正在识别并写入公共前缀；新会话前几轮 hit 为 0 属于预热期。"
	}
	if health.Status == "low" && health.ConsecutiveBelowTarget < 3 {
		health.Status = "watch"
		health.Message = "样本仍少，DeepSeek 缓存写入是 best-effort；继续用同模型、同 messages 前缀观察。"
	}
	if health.Status == "watch" && health.HitTokensIncreasing && (health.MissTokensStable || health.MissTokensImproving) {
		health.Message = "公共前缀命中正在上升，未命中 tokens 保持低位；继续同配置验证。"
	}
	return health
}

func AnalyzeSingle(m UsageMetrics) Health {
	health := Health{
		Status:           "pending",
		Message:          "等待首轮模型请求记录缓存命中数据。",
		HitRate:          m.CacheHitRate(),
		TargetHitRate:    TargetHitRate,
		HitTokens:        m.PromptCacheHitTokens,
		MissTokens:       m.PromptCacheMissTokens,
		TotalCacheTokens: m.PromptCacheHitTokens + m.PromptCacheMissTokens,
	}
	if !m.HasCacheTokens() {
		return health
	}

	health.MissTokenBudget = missBudgetForTarget(m.PromptCacheHitTokens, TargetHitRate)
	health.RequiredHitTokens = requiredHitForTarget(m.PromptCacheMissTokens, TargetHitRate)
	if health.RequiredHitTokens > m.PromptCacheHitTokens {
		health.AdditionalHitTokensNeeded = health.RequiredHitTokens - m.PromptCacheHitTokens
	}
	health.ShortPrompt = health.TotalCacheTokens < shortPromptTokenLimit

	if !m.BelowTarget() {
		health.Status = "ok"
		health.Message = "缓存命中率达到 96% 目标。"
		return health
	}
	if health.TotalCacheTokens < tinyPromptTokenLimit {
		health.Status = "watch"
		health.Message = "本轮 tokens 太少，低百分比不能代表 DeepSeek 前缀缓存异常。"
		return health
	}
	if m.PromptCacheHitTokens == 0 {
		health.Status = "warming"
		health.Message = "DeepSeek 上下文缓存正在预热；公共前缀首次写入前通常 miss 偏高。"
		return health
	}
	if health.ShortPrompt && m.PromptCacheMissTokens <= residualMissTokenLimit {
		health.Status = "watch"
		health.Message = "短请求样本偏小，少量未命中 tokens 会把百分比放大。"
		return health
	}
	if health.HitRate >= nearTargetHitRate && m.PromptCacheMissTokens <= residualMissTokenLimit {
		health.Status = "watch"
		health.Message = "DeepSeek 公共前缀已稳定命中；剩余 miss 多来自可变尾部或缓存粒度。"
		return health
	}

	health.Status = "low"
	health.Message = "缓存命中率持续低于 DeepSeek 预期，需要检查是否破坏从第 0 token 开始的公共前缀。"
	return health
}

func Diagnostics(m UsageMetrics) []string {
	return DiagnosticsHistory([]UsageMetrics{m})
}

func DiagnosticsHistory(history []UsageMetrics) []string {
	health := AnalyzeHistory(history)
	if health.Status == "pending" {
		return []string{health.Message}
	}
	if health.Status == "ok" {
		return []string{health.Message}
	}

	diagnostics := []string{}
	if health.Status == "watch" || health.Status == "cold" || health.Status == "warming" {
		diagnostics = append(diagnostics, health.Message)
	}
	diagnostics = append(diagnostics, "DeepSeek 官方缓存按从开头开始的公共前缀命中；新前缀首次写入和极短请求不应直接判为故障。")
	if health.SampleCount > 1 {
		diagnostics = append(diagnostics, "最近 "+formatInt(int64(health.SampleCount))+" 轮样本已纳入判断。")
	}
	if health.HitTokensIncreasing {
		diagnostics = append(diagnostics, "命中 tokens 连续上升，说明 DeepSeek 已开始复用公共前缀。")
	}
	if health.MissTokensStable {
		diagnostics = append(diagnostics, "未命中 tokens 维持低位，剩余 miss 多来自可变尾部或缓存粒度。")
	} else if health.MissTokensImproving {
		diagnostics = append(diagnostics, "未命中 tokens 正在下降，说明公共前缀逐步稳定。")
	}
	if health.HitTokens > 0 && health.MissTokens > 0 {
		diagnostics = append(diagnostics, "下一轮请求的未命中尾部通常包含上一轮 assistant 回复和本轮 user 输入；短会话里这会明显压低百分比。")
	}
	if health.RequiredHitTokens > 0 {
		diagnostics = append(diagnostics, "按当前未命中 tokens 计算，96% 目标需要约 "+formatInt(health.RequiredHitTokens)+" 个稳定命中 tokens。")
	}
	if health.AdditionalHitTokensNeeded > 0 {
		diagnostics = append(diagnostics, "当前还差约 "+formatInt(health.AdditionalHitTokensNeeded)+" 个稳定命中 tokens；短试聊不必只看百分比。")
	}
	if health.HitTokens > 0 {
		diagnostics = append(diagnostics, "已有公共前缀命中，说明缓存正在生效；继续保持同模型、同推理强度和同 messages 前缀。")
	} else if health.Status == "cold" || health.Status == "warming" {
		diagnostics = append(diagnostics, "当前 hit 为 0，先保持同配置重复 1-3 轮并等待数秒；若仍为 0，再检查前缀是否每轮变化。")
	}

	diagnostics = append(diagnostics,
		"检查是否重复注入完整 Skills 正文。",
		"检查 MCP schema 顺序是否稳定。",
		"检查工具结果是否已压缩为摘要。",
		"检查用户输入是否只追加在易变尾部。",
	)
	return diagnostics
}

func countSamples(history []UsageMetrics) int {
	count := 0
	for _, metrics := range history {
		if metrics.HasCacheTokens() {
			count++
		}
	}
	return count
}

func consecutiveBelowTarget(history []UsageMetrics) int {
	count := 0
	for i := len(history) - 1; i >= 0; i-- {
		metrics := history[i]
		if !metrics.HasCacheTokens() || !metrics.BelowTarget() {
			break
		}
		count++
	}
	return count
}

func hitTokensIncreasing(history []UsageMetrics) bool {
	samples := samplesWithCacheTokens(history)
	if len(samples) < 2 {
		return false
	}
	last := samples[len(samples)-1]
	prev := samples[len(samples)-2]
	return last.PromptCacheHitTokens > prev.PromptCacheHitTokens
}

func missTokensStable(history []UsageMetrics) bool {
	samples := samplesWithCacheTokens(history)
	if len(samples) < 2 {
		return false
	}
	last := samples[len(samples)-1]
	prev := samples[len(samples)-2]
	if last.PromptCacheMissTokens <= 128 && prev.PromptCacheMissTokens <= 128 {
		return true
	}
	diff := last.PromptCacheMissTokens - prev.PromptCacheMissTokens
	if diff < 0 {
		diff = -diff
	}
	return diff <= 32
}

func missTokensImproving(history []UsageMetrics) bool {
	samples := samplesWithCacheTokens(history)
	if len(samples) < 2 {
		return false
	}
	last := samples[len(samples)-1]
	prev := samples[len(samples)-2]
	return last.PromptCacheMissTokens < prev.PromptCacheMissTokens
}

func samplesWithCacheTokens(history []UsageMetrics) []UsageMetrics {
	samples := []UsageMetrics{}
	for _, metrics := range history {
		if metrics.HasCacheTokens() {
			samples = append(samples, metrics)
		}
	}
	return samples
}

func missBudgetForTarget(hitTokens int64, target float64) int64 {
	if hitTokens <= 0 || target <= 0 || target >= 1 {
		return 0
	}
	return int64(math.Floor(float64(hitTokens) * (1 - target) / target))
}

func requiredHitForTarget(missTokens int64, target float64) int64 {
	if missTokens <= 0 || target <= 0 || target >= 1 {
		return 0
	}
	return int64(math.Ceil(float64(missTokens) * target / (1 - target)))
}

func formatInt(value int64) string {
	if value < 1000 {
		return stringInt(value)
	}
	digits := stringInt(value)
	out := ""
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out += ","
		}
		out += string(r)
	}
	return out
}

func stringInt(value int64) string {
	if value == 0 {
		return "0"
	}
	out := ""
	for value > 0 {
		out = string(rune('0'+value%10)) + out
		value /= 10
	}
	return out
}
