package agent

import "testing"

func TestReasoningProfilesIncludeUltraDefault(t *testing.T) {
	profile, ok := ReasoningProfileFor(DefaultReasoningLevel)
	if !ok {
		t.Fatal("default reasoning level should have a profile")
	}
	if profile.ID != ReasoningUltra {
		t.Fatalf("default reasoning = %s, want %s", profile.ID, ReasoningUltra)
	}
	if profile.Budget.ContextPolicy != "full-relevant" || !profile.Budget.Planner {
		t.Fatalf("unexpected default reasoning budget: %#v", profile.Budget)
	}
}

func TestParseReasoningLevelRejectsUnknown(t *testing.T) {
	if _, err := ParseReasoningLevel("extreme"); err == nil {
		t.Fatal("expected unknown reasoning level error")
	}
}

func TestReasoningProfilesUsePersistedAnthropicModelCapabilities(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	settings := service.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{
		SelectedProviderID: "anthropic-relay",
		SelectedModelID:    "relay-model",
		Providers: []ModelProviderSetting{{
			ID: "anthropic-relay", Protocol: "anthropic-compatible", ReasoningProfile: "auto",
			Models: []ProviderModel{{
				ID: "relay-model", ReasoningLevels: []string{"none", "low", "high"}, ThinkingModes: []string{"adaptive"},
			}},
		}},
	}
	profiles := service.reasoningProfilesForRuntime(settings)
	if len(profiles) != 3 || profiles[0].ID != ReasoningNone || profiles[1].ID != ReasoningLow || profiles[2].ID != ReasoningHigh {
		t.Fatalf("profiles = %#v", profiles)
	}

	settings.Model.Providers[0].ReasoningProfile = "none"
	profiles = service.reasoningProfilesForRuntime(settings)
	if len(profiles) != len(ReasoningProfiles()) {
		t.Fatalf("disabled wire profile should retain agent reasoning budgets: %#v", profiles)
	}
}
