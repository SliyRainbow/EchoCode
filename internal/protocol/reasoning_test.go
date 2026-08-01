package protocol

import (
	"reflect"
	"testing"
)

func TestResolveReasoningOptionsUsesModelSpecificOpenAIMaximum(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "gpt-5.6", want: "max"},
		{model: "gpt-5.5", want: "xhigh"},
		{model: "gpt-5.4", want: "xhigh"},
		{model: "gpt-5.4-mini", want: "high"},
		{model: "gpt-5.3-codex", want: "xhigh"},
		{model: "gpt-5.1", want: "high"},
		{model: "o3", want: "high"},
		{model: "gpt-4.1", want: ""},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			got := ResolveReasoningOptions("openai-compatible", "https://api.openai.com/v1", test.model, "max")
			if got.Effort != test.want {
				t.Fatalf("effort = %q, want %q", got.Effort, test.want)
			}
		})
	}
}

func TestResolveReasoningOptionsUsesModelCapabilitiesOnRelayEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://api.openai.com/v1", "https://proxy.example/v1"} {
		got := ResolveReasoningOptions("openai-compatible", endpoint, "gpt-5.6-sol", "max")
		if got.Effort != "max" {
			t.Fatalf("%s effort = %q, want max", endpoint, got.Effort)
		}
	}
}

func TestGPT56ExposesAndPreservesEveryReasoningEffort(t *testing.T) {
	want := []string{"none", "low", "medium", "high", "xhigh", "max"}
	got := SupportedReasoningLevelsWithProfile("auto", "openai-compatible", "https://relay.example/v1", "openai/gpt-5.6-sol")
	if len(got) != len(want) {
		t.Fatalf("levels = %#v, want %#v", got, want)
	}
	for index, level := range want {
		if got[index] != level {
			t.Fatalf("levels = %#v, want %#v", got, want)
		}
		resolved := ResolveReasoningOptions("openai-compatible", "https://relay.example/v1", "gpt-5.6-sol", level)
		if resolved.Effort != level {
			t.Fatalf("level %s resolved to %#v", level, resolved)
		}
	}
}

func TestResolveReasoningOptionsWithExplicitCustomProviderProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		model   string
		level   string
		want    ReasoningOptions
	}{
		{name: "openai model aware", profile: "openai", model: "gpt-5.5", level: "max", want: ReasoningOptions{Effort: "xhigh"}},
		{name: "generic openai effort", profile: "openai-effort", model: "vendor-reasoner", level: "max", want: ReasoningOptions{Effort: "xhigh"}},
		{name: "deepseek proxy", profile: "deepseek", model: "vendor-deepseek", level: "medium", want: ReasoningOptions{Mode: "enabled", Effort: "high"}},
		{name: "disabled", profile: "none", model: "gpt-5.6", level: "max", want: ReasoningOptions{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveReasoningOptionsWithProfile(test.profile, "openai-compatible", "https://proxy.example/v1", test.model, test.level)
			if got != test.want {
				t.Fatalf("options = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveReasoningOptionsMapsNativeProviders(t *testing.T) {
	deepSeek := ResolveReasoningOptions("deepseek-official", "https://api.deepseek.com", "deepseek-v4-pro", "medium")
	if deepSeek.Mode != "enabled" || deepSeek.Effort != "high" {
		t.Fatalf("deepseek medium = %#v", deepSeek)
	}

	adaptive := ResolveReasoningOptions("anthropic", "https://api.anthropic.com", "claude-opus-4-6", "max")
	if adaptive.Mode != "adaptive" || adaptive.Effort != "max" {
		t.Fatalf("anthropic adaptive = %#v", adaptive)
	}

	budgeted := ResolveReasoningOptions("anthropic-compatible", "https://anthropic.example", "claude-sonnet-4-5", "high")
	if budgeted.Mode != "enabled" || budgeted.BudgetTokens != 8192 {
		t.Fatalf("anthropic budgeted = %#v", budgeted)
	}

	gemini := ResolveReasoningOptions("gemini", "https://generativelanguage.googleapis.com/v1beta", "gemini-3.6-flash", "max")
	if gemini.ThinkingLevel != "HIGH" {
		t.Fatalf("gemini ultra = %#v", gemini)
	}

	xai := ResolveReasoningOptions("openai-compatible", "https://api.x.ai/v1", "grok-4.20-multi-agent", "max")
	if xai.Effort != "xhigh" {
		t.Fatalf("xAI ultra = %#v", xai)
	}
}

func TestAnthropicReasoningUsesPerModelCapabilities(t *testing.T) {
	tests := []struct {
		name  string
		model string
		level string
		want  ReasoningOptions
	}{
		{name: "fable required adaptive", model: "claude-fable-5", level: "max", want: ReasoningOptions{Mode: "adaptive", Effort: "max"}},
		{name: "fable cannot be disabled", model: "claude-fable-5", level: "none", want: ReasoningOptions{}},
		{name: "opus preserves xhigh", model: "anthropic/claude-opus-5", level: "xhigh", want: ReasoningOptions{Mode: "adaptive", Effort: "xhigh"}},
		{name: "sonnet can disable", model: "claude-sonnet-5", level: "none", want: ReasoningOptions{Mode: "disabled"}},
		{name: "dated opus alias", model: "claude-opus-5-20260724", level: "high", want: ReasoningOptions{Mode: "adaptive", Effort: "high"}},
		{name: "new opus 4 adaptive", model: "claude-opus-4-8", level: "xhigh", want: ReasoningOptions{Mode: "adaptive", Effort: "xhigh"}},
		{name: "legacy sonnet budget", model: "claude-sonnet-4-5-20250929", level: "max", want: ReasoningOptions{Mode: "enabled", BudgetTokens: 16_384}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveReasoningOptions("anthropic", DefaultAnthropicBaseURL, test.model, test.level)
			if got != test.want {
				t.Fatalf("options = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAnthropicReasoningMenuReflectsDisableAndEffortSupport(t *testing.T) {
	tests := []struct {
		model string
		want  []string
	}{
		{model: "claude-fable-5", want: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "claude-opus-5", want: []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{model: "claude-sonnet-4-5", want: []string{"none", "low", "medium", "high", "xhigh", "max"}},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			got := SupportedReasoningLevelsWithProfile("auto", "anthropic", DefaultAnthropicBaseURL, test.model)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("levels = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReportedAnthropicCapabilitiesDoNotInventDisabledThinking(t *testing.T) {
	var unsupported anthropicReportedModelCapabilities
	levels := anthropicReportedReasoningLevels("custom-no-thinking", &unsupported)
	if !reflect.DeepEqual(levels, []string{"none"}) {
		t.Fatalf("unsupported levels = %#v", levels)
	}
	if got := applyReportedAnthropicReasoning(
		ReasoningOptions{Mode: "disabled"}, "none", levels, anthropicReportedThinkingModes(&unsupported),
	); got != (ReasoningOptions{}) {
		t.Fatalf("unsupported thinking request = %#v, want omitted field", got)
	}

	var adaptive anthropicReportedModelCapabilities
	adaptive.Thinking.Supported = true
	adaptive.Thinking.Types.Adaptive.Supported = true
	adaptive.Effort.Supported = true
	adaptive.Effort.Low.Supported = true
	levels = anthropicReportedReasoningLevels("custom-required-thinking", &adaptive)
	if !reflect.DeepEqual(levels, []string{"low"}) {
		t.Fatalf("adaptive levels = %#v, want no invented none option", levels)
	}
}
