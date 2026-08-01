package agent

import (
	"errors"
	"fmt"
	"strings"
)

// SetEphemeralModelRoute changes the provider/model only for an isolated
// session runtime. It deliberately does not write runtime-settings.json.
func (s *Service) SetEphemeralModelRoute(providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" && modelID == "" {
		return nil
	}
	if providerID == "" {
		return errors.New("指定自动化模型时必须同时指定供应商")
	}
	release, err := s.beginActivity("selecting an ephemeral model route")
	if err != nil {
		return err
	}
	defer release()

	settings := s.runtimeSettings.Normalized()
	provider, _, ok := findModelProvider(settings.Model.Providers, providerID)
	if !ok {
		return fmt.Errorf("模型供应商不存在: %s", providerID)
	}
	if !provider.Enabled {
		return fmt.Errorf("模型供应商未启用: %s", provider.Name)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(provider.DefaultModelID)
		if modelID == "" && len(provider.Models) > 0 {
			modelID = strings.TrimSpace(provider.Models[0].ID)
		}
	}
	if modelID == "" {
		return fmt.Errorf("供应商 %s 尚未配置模型", provider.Name)
	}
	if len(provider.Models) > 0 && !hasProviderModel(provider.Models, modelID) {
		return fmt.Errorf("供应商 %s 中不存在模型 %s", provider.Name, modelID)
	}
	settings.Model.SelectedProviderID = provider.ID
	settings.Model.SelectedModelID = modelID
	s.runtimeSettings = settings
	s.invalidateProviderSession("自动化任务使用独立模型路由。")
	return nil
}
