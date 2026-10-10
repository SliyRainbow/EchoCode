package protocol

import (
	"net/url"
	"strings"
)

// ReasoningOptions is the provider-specific wire configuration resolved from
// EchoCode's provider-independent reasoning levels.
type ReasoningOptions struct {
	Mode          string
	Effort        string
	BudgetTokens  int
	ThinkingLevel string
}

// ResolveReasoningOptions keeps the UI vocabulary provider-independent while
// only emitting values documented for the selected endpoint and model family.
func ResolveReasoningOptions(providerProtocol, baseURL, model, level string) ReasoningOptions {
	return ResolveReasoningOptionsWithProfile("auto", providerProtocol, baseURL, model, level)
}

// ResolveReasoningOptionsWithProfile applies an explicit compatibility profile
// for custom providers. "auto" only recognizes native protocols and known
// official hosts; unknown OpenAI-compatible endpoints remain untouched.
func ResolveReasoningOptionsWithProfile(profile, providerProtocol, baseURL, model, level string) ReasoningOptions {
	protocolName := strings.ToLower(strings.TrimSpace(providerProtocol))
	modelID := normalizeReasoningModelID(model)
	level = normalizeReasoningLevel(level)
	if !validReasoningLevel(level) {
		return ReasoningOptions{}
	}
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "auto"
	}
	switch profile {
	case "none":
		return ReasoningOptions{}
	case "openai":
		return openAIReasoningOptions(modelID, level)
	case "openai-effort":
		return ReasoningOptions{Effort: effortForProductLevel(level, "xhigh")}
	case "xai":
		return xAIReasoningOptions(modelID, level)
	case "deepseek":
		return deepSeekReasoningOptions(level)
	case "anthropic":
		return anthropicReasoningOptions(modelID, level)
	case "gemini":
		return geminiReasoningOptions(modelID, level)
	case "auto":
	default:
		return ReasoningOptions{}
	}

	switch protocolName {
	case "deepseek", "deepseek-official":
		return deepSeekReasoningOptions(level)
	case "anthropic", "anthropic-compatible":
		return anthropicReasoningOptions(modelID, level)
	case "gemini":
		return geminiReasoningOptions(modelID, level)
	case "openai-compatible", "local":
		// Proxies keep the upstream model id but replace the hostname. Model
		// capability detection therefore has to run before the official-host
		// shortcut or reasoning silently disappears on relays.
		if resolved := openAIReasoningOptions(modelID, level); resolved.Effort != "" {
			return resolved
		}
		if resolved := xAIReasoningOptions(modelID, level); resolved.Effort != "" {
			return resolved
		}
		host := reasoningEndpointHost(baseURL)
		switch {
		case host == "api.openai.com":
			return openAIReasoningOptions(modelID, level)
		case host == "api.x.ai" || strings.HasSuffix(host, ".x.ai"):
			return xAIReasoningOptions(modelID, level)
		}
	}
	return ReasoningOptions{}
}

// SupportedReasoningLevelsWithProfile returns the choices that are meaningful
// for the selected model. Model ids take precedence over hostnames so relays
// expose the same menu as the corresponding official endpoint.
func SupportedReasoningLevelsWithProfile(profile, providerProtocol, baseURL, model string) []string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "auto"
	}
	protocolName := strings.ToLower(strings.TrimSpace(providerProtocol))
	modelID := normalizeReasoningModelID(model)

	switch profile {
	case "openai":
		if levels := openAIReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		return standardReasoningLevels()
	case "openai-effort":
		return allReasoningLevels()
	case "xai":
		if levels := xAIReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		return []string{"low", "medium", "high", "xhigh"}
	case "deepseek":
		return []string{"none", "high", "max"}
	case "anthropic":
		if levels := anthropicReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		return allReasoningLevels()
	case "gemini":
		return []string{"none", "low", "medium", "high"}
	case "none":
		// This setting disables only the upstream parameter. Agent execution
		// budgets remain selectable.
		return allReasoningLevels()
	case "auto":
	default:
		return standardReasoningLevels()
	}

	switch protocolName {
	case "deepseek", "deepseek-official":
		return []string{"none", "high", "max"}
	case "anthropic", "anthropic-compatible":
		if levels := anthropicReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		return allReasoningLevels()
	case "gemini":
		return []string{"none", "low", "medium", "high"}
	case "openai-compatible", "local":
		if levels := openAIReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		if levels := xAIReasoningLevels(modelID); len(levels) > 0 {
			return levels
		}
		host := reasoningEndpointHost(baseURL)
		if host == "api.openai.com" {
			return standardReasoningLevels()
		}
		if host == "api.x.ai" || strings.HasSuffix(host, ".x.ai") {
			return []string{"low", "medium", "high", "xhigh"}
		}
	}
	return standardReasoningLevels()
}

func allReasoningLevels() []string {
	return []string{"none", "low", "medium", "high", "xhigh", "max"}
}

func standardReasoningLevels() []string {
	return []string{"low", "medium", "high", "max"}
}

func levelsThrough(maximum string, includeNone bool) []string {
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	limit := len(levels)
	for index, level := range levels {
		if level == maximum {
			limit = index + 1
			break
		}
	}
	result := append([]string(nil), levels[:limit]...)
	if includeNone {
		result = append([]string{"none"}, result...)
	}
	return result
}

func openAIReasoningLevels(modelID string) []string {
	maximum, supportsNone, ok := openAIReasoningCapability(modelID)
	if !ok {
		return nil
	}
	return levelsThrough(maximum, supportsNone)
}

func xAIReasoningLevels(modelID string) []string {
	maximum := xAIReasoningMaximum(modelID)
	if maximum == "" {
		return nil
	}
	return levelsThrough(maximum, false)
}

func reasoningOptionsForRequest(providerProtocol, baseURL, reasoningProfile string, request ChatRequest) ReasoningOptions {
	resolved := ResolveReasoningOptionsWithProfile(reasoningProfile, providerProtocol, baseURL, request.Model, request.ReasoningLevel)
	if mode := strings.TrimSpace(request.ThinkingMode); mode != "" {
		resolved.Mode = mode
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		resolved.Effort = effort
	}
	return resolved
}

func deepSeekReasoningOptions(level string) ReasoningOptions {
	switch level {
	case "none", "low":
		return ReasoningOptions{Mode: "disabled"}
	case "medium", "high":
		// DeepSeek currently exposes high/max. Its documented compatibility
		// behavior also maps low and medium effort values to high.
		return ReasoningOptions{Mode: "enabled", Effort: "high"}
	case "xhigh", "max":
		return ReasoningOptions{Mode: "enabled", Effort: "max"}
	default:
		return ReasoningOptions{}
	}
}

func openAIReasoningOptions(modelID, level string) ReasoningOptions {
	if modelID == "" || strings.Contains(modelID, "chat-latest") {
		return ReasoningOptions{}
	}
	maximum, supportsNone, ok := openAIReasoningCapability(modelID)
	if !ok || level == "none" && !supportsNone {
		return ReasoningOptions{}
	}
	return ReasoningOptions{Effort: effortForProductLevel(level, maximum)}
}

func openAIReasoningCapability(modelID string) (maximum string, supportsNone bool, ok bool) {
	switch {
	case modelFamily(modelID, "gpt-6-astra"), modelFamily(modelID, "gpt-6.1-sol"):
		return "max", false, true
	case modelFamily(modelID, "gpt-6-sol"), modelFamily(modelID, "gpt-6-luna"):
		return "max", true, true
	case modelFamily(modelID, "gpt-5.6"):
		return "max", true, true
	case modelFamily(modelID, "gpt-5.5"),
		modelFamily(modelID, "gpt-5.4") && !strings.Contains(modelID, "-mini") && !strings.Contains(modelID, "-nano"),
		modelFamily(modelID, "gpt-5.3-codex"),
		modelFamily(modelID, "gpt-5.2"),
		modelFamily(modelID, "gpt-5.1-codex-max"):
		return "xhigh", false, true
	case modelFamily(modelID, "gpt-5"),
		strings.HasPrefix(modelID, "gpt-5"),
		modelFamily(modelID, "o1"),
		modelFamily(modelID, "o3"),
		modelFamily(modelID, "o4"),
		modelFamily(modelID, "codex-mini-latest"),
		modelFamily(modelID, "gpt-oss"):
		return "high", false, true
	default:
		return "", false, false
	}
}

func xAIReasoningOptions(modelID, level string) ReasoningOptions {
	maximum := xAIReasoningMaximum(modelID)
	if maximum == "" {
		return ReasoningOptions{}
	}
	return ReasoningOptions{Effort: effortForProductLevel(level, maximum)}
}

func xAIReasoningMaximum(modelID string) string {
	switch {
	case modelFamily(modelID, "grok-4.5"):
		return "high"
	case strings.HasPrefix(modelID, "grok-4.20") && !strings.Contains(modelID, "non-reasoning"):
		return "xhigh"
	default:
		return ""
	}
}

func anthropicReasoningOptions(modelID, level string) ReasoningOptions {
	capability, known := anthropicModelCapabilities(modelID)
	if !known || capability.Thinking == anthropicThinkingUnsupported {
		return ReasoningOptions{}
	}
	if level == "none" {
		if capability.AllowsDisabled {
			return ReasoningOptions{Mode: "disabled"}
		}
		return ReasoningOptions{}
	}
	if capability.Thinking == anthropicThinkingAdaptive {
		return ReasoningOptions{Mode: "adaptive", Effort: effortForProductLevel(level, capability.MaximumEffort)}
	}
	return ReasoningOptions{Mode: "enabled", BudgetTokens: anthropicBudgetForEffort(level)}
}

func geminiReasoningOptions(modelID, level string) ReasoningOptions {
	if !strings.HasPrefix(modelID, "gemini-2.5") && !strings.HasPrefix(modelID, "gemini-3") {
		return ReasoningOptions{}
	}

	if level == "none" {
		return ReasoningOptions{}
	}
	thinkingLevel := map[string]string{
		"low":    "LOW",
		"medium": "MEDIUM",
		"high":   "HIGH",
		"xhigh":  "HIGH",
		"max":    "HIGH",
	}[level]
	// These model cards expose a narrower set than the other Gemini thinking
	// models, so clamp to a supported value instead of sending an invalid enum.
	if strings.HasPrefix(modelID, "gemini-3-pro-preview") && level == "medium" {
		thinkingLevel = "LOW"
	}
	if strings.HasPrefix(modelID, "gemini-3.1-flash-lite-image") {
		if level == "low" {
			thinkingLevel = "MINIMAL"
		} else {
			thinkingLevel = "HIGH"
		}
	}
	return ReasoningOptions{ThinkingLevel: thinkingLevel}
}

func effortForProductLevel(level, maximum string) string {
	level = normalizeReasoningLevel(level)
	ranks := map[string]int{"none": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}
	levelRank, levelOK := ranks[level]
	maximumRank, maximumOK := ranks[maximum]
	if !levelOK || !maximumOK {
		return ""
	}
	if levelRank > maximumRank {
		return maximum
	}
	return level
}

func validReasoningLevel(level string) bool {
	return level == "none" || level == "low" || level == "medium" || level == "high" || level == "xhigh" || level == "max"
}

func normalizeReasoningLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "ultra" {
		return "max"
	}
	return level
}

func normalizeReasoningModelID(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "models/")
	for _, prefix := range []string{"openai/", "xai/", "x-ai/", "anthropic/", "google/", "deepseek/"} {
		model = strings.TrimPrefix(model, prefix)
	}
	return model
}

func modelFamily(modelID, family string) bool {
	return modelID == family || strings.HasPrefix(modelID, family+"-")
}

func reasoningEndpointHost(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.TrimSuffix(host, ".")
}
