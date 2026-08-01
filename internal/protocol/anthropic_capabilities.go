package protocol

import "strings"

var anthropicOptionalParameters = []string{"temperature", "thinking", "output_config"}

type anthropicThinkingKind uint8

const (
	anthropicThinkingUnsupported anthropicThinkingKind = iota
	anthropicThinkingBudgeted
	anthropicThinkingAdaptive
)

type anthropicModelCapability struct {
	Thinking             anthropicThinkingKind
	AllowsDisabled       bool
	MaximumDisableEffort string
	RejectsSampling      bool
	MaximumEffort        string
	MaxOutputTokens      int
	ContextWindowTokens  int
}

// anthropicModelCapabilities is deliberately model-aware. Anthropic changes
// thinking, effort, sampling and token limits independently, so a broad
// "claude-*" fallback can easily produce an invalid request.
func anthropicModelCapabilities(modelID string) (anthropicModelCapability, bool) {
	id := normalizeReasoningModelID(modelID)
	switch {
	case anthropicModelAlias(id, "claude-fable-5"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-mythos-5"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-mythos-preview"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-opus-5"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, AllowsDisabled: true, MaximumDisableEffort: "high", MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-sonnet-5"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, AllowsDisabled: true, MaximumDisableEffort: "max", MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-opus-4-8"), anthropicModelAlias(id, "claude-opus-4-7"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, AllowsDisabled: true, MaximumDisableEffort: "max", MaximumEffort: "max",
			RejectsSampling: true, MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-opus-4-6"), anthropicModelAlias(id, "claude-sonnet-4-6"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingAdaptive, AllowsDisabled: true, MaximumDisableEffort: "max", MaximumEffort: "max",
			MaxOutputTokens: 128_000,
		}, true
	case anthropicModelAlias(id, "claude-haiku-4-5"),
		anthropicModelAlias(id, "claude-opus-4-5"),
		anthropicModelAlias(id, "claude-sonnet-4-5"),
		anthropicModelAlias(id, "claude-opus-4-1"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingBudgeted, AllowsDisabled: true, MaximumDisableEffort: "max", MaximumEffort: "max",
			ContextWindowTokens: 200_000,
		}, true
	case anthropicModelAlias(id, "claude-opus-4"),
		anthropicModelAlias(id, "claude-sonnet-4"),
		anthropicModelAlias(id, "claude-3-7-sonnet"):
		return anthropicModelCapability{
			Thinking: anthropicThinkingBudgeted, AllowsDisabled: true, MaximumDisableEffort: "max", MaximumEffort: "max",
			ContextWindowTokens: 200_000,
		}, true
	default:
		return anthropicModelCapability{}, false
	}
}

// AnthropicModelContextWindow returns only catalog values tied to a known
// Anthropic model identity. Unknown Anthropic-compatible names intentionally do
// not inherit a blanket 200K claim.
func AnthropicModelContextWindow(modelID string) (int, bool) {
	capability, known := anthropicModelCapabilities(modelID)
	if !known || capability.ContextWindowTokens <= 0 {
		return 0, false
	}
	return capability.ContextWindowTokens, true
}

func anthropicReasoningLevels(modelID string) []string {
	capability, known := anthropicModelCapabilities(modelID)
	if !known {
		return nil
	}
	if capability.Thinking == anthropicThinkingUnsupported {
		return []string{"none"}
	}
	return levelsThrough(capability.MaximumEffort, capability.AllowsDisabled)
}

func anthropicReportedReasoningLevels(modelID string, reported *anthropicReportedModelCapabilities) []string {
	if reported == nil {
		return nil
	}
	capability, known := anthropicModelCapabilities(modelID)
	thinkingSupported := reported.Thinking.Supported ||
		reported.Thinking.Types.Adaptive.Supported || reported.Thinking.Types.Enabled.Supported
	if !thinkingSupported {
		return []string{"none"}
	}
	levels := make([]string, 0, 6)
	if known && capability.AllowsDisabled {
		levels = append(levels, "none")
	}
	if reported.Effort.Supported || anthropicReportedEffortAvailable(reported) {
		for _, candidate := range []struct {
			level     string
			supported bool
		}{
			{level: "low", supported: reported.Effort.Low.Supported},
			{level: "medium", supported: reported.Effort.Medium.Supported},
			{level: "high", supported: reported.Effort.High.Supported},
			{level: "xhigh", supported: reported.Effort.XHigh.Supported},
			{level: "max", supported: reported.Effort.Max.Supported},
		} {
			if candidate.supported {
				levels = append(levels, candidate.level)
			}
		}
	} else if reported.Thinking.Types.Enabled.Supported {
		// Manual extended thinking has no effort enum. EchoCode maps its product
		// levels to documented budget_tokens values.
		levels = append(levels, "low", "medium", "high", "xhigh", "max")
	}
	return levels
}

func anthropicReportedThinkingModes(reported *anthropicReportedModelCapabilities) []string {
	if reported == nil {
		return nil
	}
	modes := make([]string, 0, 2)
	if reported.Thinking.Types.Adaptive.Supported {
		modes = append(modes, "adaptive")
	}
	if reported.Thinking.Types.Enabled.Supported {
		modes = append(modes, "enabled")
	}
	return modes
}

func anthropicReportedEffortAvailable(reported *anthropicReportedModelCapabilities) bool {
	return reported != nil && (reported.Effort.Low.Supported || reported.Effort.Medium.Supported ||
		reported.Effort.High.Supported || reported.Effort.XHigh.Supported || reported.Effort.Max.Supported)
}

func anthropicReasoningForRequest(baseURL, reasoningProfile string, request ChatRequest) ReasoningOptions {
	requested := reasoningOptionsForRequest("anthropic", baseURL, reasoningProfile, request)
	requested = constrainAnthropicReasoning(request.Model, requested)
	profile := strings.ToLower(strings.TrimSpace(reasoningProfile))
	if profile != "" && profile != "auto" && profile != "anthropic" {
		return requested
	}
	return applyReportedAnthropicReasoning(
		requested,
		request.ReasoningLevel,
		request.ModelReasoningLevels,
		request.ModelThinkingModes,
	)
}

func applyReportedAnthropicReasoning(requested ReasoningOptions, productLevel string, levels, modes []string) ReasoningOptions {
	levels = normalizedReasoningLevels(levels)
	modes = normalizedAnthropicThinkingModes(modes)
	if len(levels) == 0 && len(modes) == 0 {
		return requested
	}

	desired := normalizeReasoningLevel(productLevel)
	if requested.Mode == "disabled" || desired == "none" {
		// A model reporting no thinking types means "none" is represented by
		// omitting the thinking field, not by inventing thinking.type=disabled.
		if len(modes) == 0 {
			return ReasoningOptions{}
		}
		if containsString(levels, "none") {
			return ReasoningOptions{Mode: "disabled"}
		}
		return ReasoningOptions{}
	}

	effort := normalizeReasoningLevel(requested.Effort)
	if effort == "" {
		effort = desired
	}
	effort = nearestReportedReasoningEffort(effort, levels)
	if effort == "" {
		return ReasoningOptions{}
	}

	mode := requested.Mode
	if mode == "" {
		if containsString(modes, "adaptive") {
			mode = "adaptive"
		} else if containsString(modes, "enabled") {
			mode = "enabled"
		}
	}
	if mode == "adaptive" && !containsString(modes, "adaptive") && len(modes) > 0 {
		mode = "enabled"
	}
	if mode == "enabled" && !containsString(modes, "enabled") && containsString(modes, "adaptive") {
		mode = "adaptive"
	}

	switch mode {
	case "adaptive":
		return ReasoningOptions{Mode: "adaptive", Effort: effort}
	case "enabled":
		budget := requested.BudgetTokens
		if budget <= 0 {
			budget = anthropicBudgetForEffort(effort)
		}
		if budget > 0 {
			return ReasoningOptions{Mode: "enabled", BudgetTokens: budget}
		}
	}
	return ReasoningOptions{}
}

func normalizedReasoningLevels(levels []string) []string {
	result := make([]string, 0, len(levels))
	seen := map[string]bool{}
	for _, level := range levels {
		level = normalizeReasoningLevel(level)
		if !validReasoningLevel(level) || seen[level] {
			continue
		}
		seen[level] = true
		result = append(result, level)
	}
	return result
}

func normalizedAnthropicThinkingModes(modes []string) []string {
	result := make([]string, 0, len(modes))
	seen := map[string]bool{}
	for _, mode := range modes {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if mode != "adaptive" && mode != "enabled" || seen[mode] {
			continue
		}
		seen[mode] = true
		result = append(result, mode)
	}
	return result
}

func nearestReportedReasoningEffort(desired string, levels []string) string {
	order := []string{"low", "medium", "high", "xhigh", "max"}
	desiredIndex := -1
	for index, level := range order {
		if level == desired {
			desiredIndex = index
			break
		}
	}
	if desiredIndex < 0 {
		return ""
	}
	for index := desiredIndex; index >= 0; index-- {
		if containsString(levels, order[index]) {
			return order[index]
		}
	}
	for index := desiredIndex + 1; index < len(order); index++ {
		if containsString(levels, order[index]) {
			return order[index]
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func constrainAnthropicReasoning(modelID string, requested ReasoningOptions) ReasoningOptions {
	capability, known := anthropicModelCapabilities(modelID)
	if !known {
		if requested.Mode == "disabled" {
			return ReasoningOptions{}
		}
		return requested
	}
	if capability.Thinking == anthropicThinkingUnsupported || requested.Mode == "" {
		return ReasoningOptions{}
	}
	if requested.Mode == "disabled" {
		if capability.AllowsDisabled {
			effort := normalizeReasoningLevel(requested.Effort)
			if effort != "" && effortForProductLevel(effort, capability.MaximumDisableEffort) != effort {
				return ReasoningOptions{
					Mode:   "adaptive",
					Effort: effortForProductLevel(effort, capability.MaximumEffort),
				}
			}
			return ReasoningOptions{Mode: "disabled"}
		}
		// Required-thinking models use their documented default when an old
		// session still asks for an unsupported disabled mode.
		return ReasoningOptions{}
	}

	if capability.Thinking == anthropicThinkingAdaptive {
		return ReasoningOptions{
			Mode:   "adaptive",
			Effort: effortForProductLevel(requested.Effort, capability.MaximumEffort),
		}
	}

	budget := requested.BudgetTokens
	if budget <= 0 {
		budget = anthropicBudgetForEffort(requested.Effort)
	}
	if budget <= 0 {
		return ReasoningOptions{}
	}
	return ReasoningOptions{Mode: "enabled", BudgetTokens: budget}
}

func anthropicBudgetForEffort(effort string) int {
	return map[string]int{
		"low": 1_024, "medium": 4_096, "high": 8_192,
		"xhigh": 12_288, "max": 16_384,
	}[normalizeReasoningLevel(effort)]
}

func anthropicRejectsSampling(modelID string) bool {
	capability, known := anthropicModelCapabilities(modelID)
	return known && capability.RejectsSampling
}

func anthropicShouldOmitTemperature(modelID string, reasoning ReasoningOptions) bool {
	return anthropicRejectsSampling(modelID) || reasoning.Mode != "" && reasoning.Mode != "disabled"
}

func anthropicModelMaxOutputTokens(modelID string) int {
	capability, known := anthropicModelCapabilities(modelID)
	if !known {
		return 0
	}
	return capability.MaxOutputTokens
}

func anthropicModelAlias(modelID, alias string) bool {
	if modelID == alias || modelID == alias+"-latest" {
		return true
	}
	suffix := strings.TrimPrefix(modelID, alias+"-")
	if len(suffix) != 8 || suffix == modelID {
		return false
	}
	for _, char := range suffix {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func normalizeAnthropicUnsupportedParameters(parameters []string) []string {
	seen := make(map[string]bool, len(parameters))
	for _, parameter := range parameters {
		parameter = strings.ToLower(strings.TrimSpace(parameter))
		if parameter == "effort" {
			parameter = "output_config"
		}
		for _, allowed := range anthropicOptionalParameters {
			if parameter == allowed {
				seen[parameter] = true
				break
			}
		}
	}
	result := make([]string, 0, len(seen))
	for _, parameter := range anthropicOptionalParameters {
		if seen[parameter] {
			result = append(result, parameter)
		}
	}
	return result
}

func applyAnthropicUnsupportedParameters(body *anthropicMessagesRequest, parameters []string) {
	if body == nil {
		return
	}
	for _, parameter := range normalizeAnthropicUnsupportedParameters(parameters) {
		switch parameter {
		case "temperature":
			body.Temperature = 0
		case "thinking":
			body.Thinking = nil
			// effort is coupled to adaptive thinking on current Anthropic APIs.
			body.OutputConfig = nil
		case "output_config":
			body.OutputConfig = nil
		}
	}
}

func anthropicUnsupportedParametersFromError(status int, detail string) []string {
	if status != 400 && status != 422 {
		return nil
	}
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" || !containsAnthropicCompatibilityMarker(detail) {
		return nil
	}
	parameters := make([]string, 0, 3)
	if strings.Contains(detail, "temperature") {
		parameters = append(parameters, "temperature")
	}
	if strings.Contains(detail, "thinking") || strings.Contains(detail, "budget_tokens") || strings.Contains(detail, "budget tokens") {
		parameters = append(parameters, "thinking")
	}
	if strings.Contains(detail, "output_config") || strings.Contains(detail, "output config") || strings.Contains(detail, "effort") {
		parameters = append(parameters, "output_config")
	}
	return normalizeAnthropicUnsupportedParameters(parameters)
}

func containsAnthropicCompatibilityMarker(detail string) bool {
	for _, marker := range []string{
		"deprecated", "not supported", "unsupported", "does not support",
		"not allowed", "not permitted", "unknown field", "unknown parameter",
		"unrecognized", "unexpected field", "extra inputs", "extra fields",
		"should not be provided", "is not accepted", "does not accept",
	} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func filterAnthropicSentParameters(parameters []string, sent map[string]bool, alreadyUnsupported []string) []string {
	existing := make(map[string]bool, len(alreadyUnsupported))
	for _, parameter := range normalizeAnthropicUnsupportedParameters(alreadyUnsupported) {
		existing[parameter] = true
	}
	filtered := make([]string, 0, len(parameters))
	for _, parameter := range normalizeAnthropicUnsupportedParameters(parameters) {
		if sent[parameter] && !existing[parameter] {
			filtered = append(filtered, parameter)
		}
	}
	return filtered
}
