package agent

import (
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

const (
	ContextWindowSourceUpstream = "upstream"
	ContextWindowSourceCatalog  = "catalog"
	ContextWindowSourceProtocol = "protocol-default"
	ContextWindowSourceProvider = "provider-default"
	ContextWindowSourceManual   = "manual"
	ContextWindowSourceFallback = "safe-default"

	safeDefaultContextWindowTokens = 64 * 1024
)

// Keep this table deliberately exact. Provider aliases and family prefixes are
// not enough evidence to claim a model's real context window.
var exactModelContextWindows = map[string]int{
	"gpt-5":                   400_000,
	"gpt-5-mini":              400_000,
	"gpt-5-nano":              400_000,
	"gpt-5-pro":               400_000,
	"gpt-5-chat-latest":       128_000,
	"gpt-5.1":                 400_000,
	"gpt-5.1-chat-latest":     128_000,
	"gpt-5.1-codex":           400_000,
	"gpt-5.1-codex-mini":      400_000,
	"gpt-5.1-codex-max":       400_000,
	"gpt-5.2":                 400_000,
	"gpt-5.2-2025-12-11":      400_000,
	"gpt-5.2-chat-latest":     128_000,
	"gpt-5.2-pro":             400_000,
	"gpt-5.2-pro-2025-12-11":  400_000,
	"gpt-5.2-codex":           400_000,
	"gpt-5.3-codex":           400_000,
	"gpt-5.4":                 1_050_000,
	"gpt-5.4-2026-03-05":      1_050_000,
	"gpt-5.4-pro":             1_050_000,
	"gpt-5.4-mini":            400_000,
	"gpt-5.4-nano":            400_000,
	"gpt-5.5":                 1_050_000,
	"gpt-5.6":                 1_050_000,
	"gpt-5.6-luna":            1_050_000,
	"gpt-5.6-sol":             1_050_000,
	"gpt-5.6-terra":           1_050_000,
	"gpt-4.1":                 1_047_576,
	"gpt-4.1-mini":            1_047_576,
	"gpt-4.1-nano":            1_047_576,
	"gpt-4o":                  128_000,
	"gpt-4o-mini":             128_000,
	"gpt-4o-audio-preview":    128_000,
	"gpt-4o-realtime-preview": 128_000,
	"chatgpt-4o-latest":       128_000,
	"gpt-4-turbo":             128_000,
	"gpt-3.5-turbo":           16_385,
	"gpt-3.5-turbo-0125":      16_385,
	"o1":                      200_000,
	"o1-pro":                  200_000,
	"o1-mini":                 128_000,
	"o3":                      200_000,
	"o3-pro":                  200_000,
	"o3-mini":                 200_000,
	"o4-mini":                 200_000,
	"deepseek-chat":           128_000,
	"deepseek-reasoner":       128_000,
	// xAI model cards, verified against https://docs.x.ai/developers/models on 2026-07-19.
	"grok-4.5":                                         500_000,
	"grok-4.5-latest":                                  500_000,
	"grok-build-latest":                                500_000,
	"grok-4.3":                                         1_000_000,
	"grok-4.3-latest":                                  1_000_000,
	"grok-latest":                                      1_000_000,
	"grok-4.20-0309-reasoning":                         1_000_000,
	"grok-4.20-reasoning-latest":                       1_000_000,
	"grok-4.20":                                        1_000_000,
	"grok-4.20-reasoning":                              1_000_000,
	"grok-4.20-0309":                                   1_000_000,
	"grok-4.20-beta-0309-reasoning":                    1_000_000,
	"grok-4.20-beta":                                   1_000_000,
	"grok-4.20-beta-0309":                              1_000_000,
	"grok-4.20-beta-latest":                            1_000_000,
	"grok-4.20-beta-latest-reasoning":                  1_000_000,
	"grok-4.20-beta-reasoning":                         1_000_000,
	"grok-4.20-experimental-beta-0304-reasoning":       1_000_000,
	"grok-4.20-experimental-beta-0304":                 1_000_000,
	"grok-4.20-experimental-beta-reasoning-latest":     1_000_000,
	"grok-4.20-experimental-beta-latest":               1_000_000,
	"grok-4.20-reasoning-gv2":                          1_000_000,
	"grok-4.20-0309-non-reasoning":                     1_000_000,
	"grok-4.20-non-reasoning":                          1_000_000,
	"grok-4.20-non-reasoning-latest":                   1_000_000,
	"grok-4.20-beta-non-reasoning":                     1_000_000,
	"grok-4.20-beta-latest-non-reasoning":              1_000_000,
	"grok-4.20-experimental-beta-0304-non-reasoning":   1_000_000,
	"grok-4.20-experimental-beta-non-reasoning-latest": 1_000_000,
	"grok-4.20-beta-0309-non-reasoning":                1_000_000,
	"grok-4.20-non-reasoning-gv2":                      1_000_000,
	"grok-4.20-multi-agent-0309":                       1_000_000,
	"grok-4.20-multi-agent":                            1_000_000,
	"grok-4.20-multi-agent-latest":                     1_000_000,
	"grok-4.20-multi-agent-beta-latest":                1_000_000,
	"grok-4.20-multi-agent-experimental-beta-0304":     1_000_000,
	"grok-4.20-multi-agent-experimental-beta-latest":   1_000_000,
	"grok-4.20-multi-agent-beta-0309":                  1_000_000,
	"grok-build-0.1":                                   256_000,
	"grok-code-fast-1":                                 256_000,
	"grok-code-fast":                                   256_000,
	"grok-code-fast-1-0825":                            256_000,
}

func resolveProviderModelContexts(provider ModelProviderSetting, models []protocol.Model) []protocol.Model {
	existing := make(map[string]ProviderModel, len(provider.Models))
	for _, model := range provider.Models {
		existing[strings.TrimSpace(model.ID)] = model
	}

	resolved := make([]protocol.Model, 0, len(models))
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		previous, hasPrevious := existing[model.ID]
		if hasPrevious {
			if strings.TrimSpace(previous.DisplayName) != "" && previous.DisplayName != previous.ID {
				model.DisplayName = previous.DisplayName
			}
			if model.MaxOutputTokens <= 0 {
				model.MaxOutputTokens = previous.MaxOutputTokens
			}
			if len(model.ReasoningLevels) == 0 {
				model.ReasoningLevels = append([]string(nil), previous.ReasoningLevels...)
			}
			if len(model.ThinkingModes) == 0 {
				model.ThinkingModes = append([]string(nil), previous.ThinkingModes...)
			}
			if len(model.UnsupportedParameters) == 0 {
				model.UnsupportedParameters = append([]string(nil), previous.UnsupportedParameters...)
			}
		}
		if model.DisplayName == "" {
			model.DisplayName = model.ID
		}
		if model.Provider == "" {
			model.Provider = provider.ID
		}

		if hasPrevious && previous.ContextWindowTokens > 0 && normalizeContextWindowSource(previous.ContextWindowSource) == ContextWindowSourceManual {
			model.ContextWindowTokens = previous.ContextWindowTokens
			model.ContextWindowSource = ContextWindowSourceManual
			resolved = append(resolved, model)
			continue
		}

		incomingSource := normalizeContextWindowSource(model.ContextWindowSource)
		if model.ContextWindowTokens > 0 && (incomingSource == ContextWindowSourceManual || incomingSource == ContextWindowSourceUpstream || incomingSource == "") {
			if incomingSource == ContextWindowSourceManual {
				model.ContextWindowSource = ContextWindowSourceManual
			} else {
				model.ContextWindowSource = ContextWindowSourceUpstream
			}
			resolved = append(resolved, model)
			continue
		}
		if hasPrevious && previous.ContextWindowTokens > 0 && normalizeContextWindowSource(previous.ContextWindowSource) == ContextWindowSourceUpstream {
			model.ContextWindowTokens = previous.ContextWindowTokens
			model.ContextWindowSource = ContextWindowSourceUpstream
			resolved = append(resolved, model)
			continue
		}

		// Recompute inferred values so older broad prefix matches are migrated.
		model.ContextWindowTokens = 0
		model.ContextWindowSource = ""

		tokens, source := inferModelContextWindow(model.ID, provider.Protocol)
		if tokens > 0 && source == ContextWindowSourceCatalog {
			model.ContextWindowTokens = tokens
			model.ContextWindowSource = source
		} else if provider.ContextWindowTokens > 0 {
			model.ContextWindowTokens = provider.ContextWindowTokens
			model.ContextWindowSource = ContextWindowSourceProvider
		} else if tokens > 0 {
			model.ContextWindowTokens = tokens
			model.ContextWindowSource = source
		} else {
			model.ContextWindowTokens = safeDefaultContextWindowTokens
			model.ContextWindowSource = ContextWindowSourceFallback
		}
		resolved = append(resolved, model)
	}
	return resolved
}

func inferModelContextWindow(modelID string, providerProtocol string) (int, string) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	protocolName := strings.ToLower(strings.TrimSpace(providerProtocol))

	if tokens, ok := protocol.AnthropicModelContextWindow(id); ok {
		return tokens, ContextWindowSourceCatalog
	}
	if tokens, ok := exactModelContextWindows[id]; ok {
		return tokens, ContextWindowSourceCatalog
	}

	switch protocolName {
	case "deepseek-official":
		return 128_000, ContextWindowSourceProtocol
	case "anthropic", "anthropic-compatible":
		return 0, ""
	case "gemini":
		return 1_048_576, ContextWindowSourceProtocol
	default:
		return 0, ""
	}
}

func unverifiedAnthropicCatalogContext(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	id = strings.TrimPrefix(id, "anthropic/")
	for _, alias := range []string{"claude-fable-5", "claude-mythos-5", "claude-opus-5", "claude-sonnet-5"} {
		if id == alias || id == alias+"-latest" {
			return true
		}
		suffix := strings.TrimPrefix(id, alias+"-")
		if len(suffix) != 8 || suffix == id {
			continue
		}
		dated := true
		for _, char := range suffix {
			if char < '0' || char > '9' {
				dated = false
				break
			}
		}
		if dated {
			return true
		}
	}
	return false
}
