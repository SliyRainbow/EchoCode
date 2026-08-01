package agent

import "testing"

func TestSetEphemeralModelRouteDoesNotPersistGlobalSettings(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "primary",
		SelectedModelID:    "model-a",
		Providers: []ModelProviderSetting{
			{ID: "primary", Name: "Primary", Protocol: "local", Enabled: true, Models: []ProviderModel{{ID: "model-a"}}},
			{ID: "worker", Name: "Worker", Protocol: "local", Enabled: true, Models: []ProviderModel{{ID: "model-b"}}},
		},
	}

	runtime, err := service.NewProjectSessionRuntime("", "session-automation")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetEphemeralModelRoute("worker", "model-b"); err != nil {
		t.Fatalf("SetEphemeralModelRoute() error = %v", err)
	}
	if runtime.runtimeSettings.Model.SelectedProviderID != "worker" || runtime.runtimeSettings.Model.SelectedModelID != "model-b" {
		t.Fatalf("runtime route = %+v", runtime.runtimeSettings.Model)
	}
	if service.runtimeSettings.Model.SelectedProviderID != "primary" || service.runtimeSettings.Model.SelectedModelID != "model-a" {
		t.Fatalf("global route was mutated: %+v", service.runtimeSettings.Model)
	}
}

func TestSetEphemeralModelRouteRejectsUnknownOrDisabledProvider(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "primary",
		SelectedModelID:    "model-a",
		Providers: []ModelProviderSetting{
			{ID: "primary", Name: "Primary", Protocol: "local", Enabled: true, Models: []ProviderModel{{ID: "model-a"}}},
			{ID: "disabled", Name: "Disabled", Protocol: "local", Enabled: false, Models: []ProviderModel{{ID: "model-b"}}},
		},
	}
	if err := service.SetEphemeralModelRoute("missing", "model-b"); err == nil {
		t.Fatal("unknown provider should be rejected")
	}
	if err := service.SetEphemeralModelRoute("disabled", "model-b"); err == nil {
		t.Fatal("disabled provider should be rejected")
	}
}
