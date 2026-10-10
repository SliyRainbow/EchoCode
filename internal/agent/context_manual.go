package agent

import (
	"fmt"
	"strings"
)

// ContextCompressionResult reports a change to the model's context view. Raw
// conversation events and file checkpoints remain available to history/rewind.
type ContextCompressionResult struct {
	ProjectID       string         `json:"projectId"`
	SessionID       string         `json:"sessionId"`
	Compressed      bool           `json:"compressed"`
	BeforeTokens    int            `json:"beforeTokens"`
	AfterTokens     int            `json:"afterTokens"`
	RemovedMessages int            `json:"removedMessages"`
	State           WorkbenchState `json:"state"`
}

// contextRoute resolves metadata without needing an API key or making a model
// request. Reading telemetry must never change provider connection status.
// The caller holds stateMu, at least for reading.
func (s *Service) contextRoute() (chatRoute, error) {
	settings := s.runtimeSettings.Normalized()
	provider, _, ok := findModelProvider(settings.Model.Providers, settings.Model.SelectedProviderID)
	if !ok {
		provider, ok = firstUsableProvider(settings.Model.Providers)
	}
	if !ok {
		return chatRoute{}, fmt.Errorf("请先选择模型")
	}
	modelID := settings.Model.SelectedModelID
	if settings.Model.SelectedProviderID != provider.ID || modelID == "" {
		modelID = provider.DefaultModelID
	}
	if modelID == "" && len(provider.Models) > 0 {
		modelID = provider.Models[0].ID
	}
	if modelID == "" {
		return chatRoute{}, fmt.Errorf("请先选择模型")
	}
	model, _ := providerModelByID(provider.Models, modelID)
	return chatRoute{Provider: provider, ModelID: modelID, Model: model}, nil
}

func (s *Service) CompressSessionContext(projectID, sessionID string) (ContextCompressionResult, error) {
	result := ContextCompressionResult{ProjectID: projectID, SessionID: sessionID}
	release, err := s.beginActivity("compressing conversation context")
	if err != nil {
		return result, err
	}
	defer release()
	activeProject, activeSession := s.activeSessionIDsLocked()
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(sessionID) == "" || projectID != activeProject || sessionID != activeSession {
		return result, fmt.Errorf("当前会话已变化，请在目标会话中重新压缩")
	}
	if s.sessionLoadError != nil {
		return result, s.sessionLoadError
	}
	route, err := s.contextRoute()
	if err != nil {
		return result, err
	}
	beforeMessages, beforeState := cloneProtocolMessages(s.sessionMessages), s.sessionState
	compressed, err := s.compressSessionContext(route, true)
	if err == nil && compressed.Compressed {
		err = s.persistContextCondensation(compressed, s.sessionMessages)
	}
	if err != nil {
		s.sessionMessages, s.sessionState = beforeMessages, beforeState
		return result, err
	}
	result.Compressed, result.BeforeTokens, result.AfterTokens = compressed.Compressed, compressed.BeforeTokens, compressed.AfterTokens
	result.RemovedMessages = compressed.RemovedMessages
	result.State = s.workbenchStateLocked()
	return result, nil
}
