package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	secretResultServiceName = "EchoCode/secret-results"
	maxSecretResultBytes    = 2048
)

type storedSecretResult struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	Value     string `json:"value"`
	CreatedAt string `json:"createdAt"`
}

type SecretResultReveal struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	Value     string `json:"value"`
	CreatedAt string `json:"createdAt"`
}

func (s *Service) storeSecretResult(label, source, value string) (tools.ResultPart, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return tools.ResultPart{}, fmt.Errorf("captured secret value is empty")
	}
	if !utf8.ValidString(value) {
		return tools.ResultPart{}, fmt.Errorf("captured secret value is not valid UTF-8 text")
	}
	if len([]byte(value)) > maxSecretResultBytes {
		return tools.ResultPart{}, fmt.Errorf("captured secret value exceeds %d bytes; make the command print only the requested value", maxSecretResultBytes)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "远程敏感结果"
	}
	label = truncateRunes(label, 120)
	source = truncateRunes(strings.TrimSpace(source), 240)

	id, err := newSecretResultID()
	if err != nil {
		return tools.ResultPart{}, err
	}
	record := storedSecretResult{
		ID:        id,
		ProjectID: strings.TrimSpace(s.projectID),
		SessionID: strings.TrimSpace(s.sessionID),
		Label:     label,
		Source:    source,
		Value:     value,
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return tools.ResultPart{}, fmt.Errorf("encode secret result: %w", err)
	}
	if err := s.secretVault.Set(secretResultServiceName, id, string(encoded)); err != nil {
		return tools.ResultPart{}, fmt.Errorf("store secret result: %w", err)
	}
	return tools.ResultPart{
		Kind:         tools.PartSecretResult,
		Status:       "ok",
		SecretID:     id,
		SecretLabel:  label,
		SecretSource: source,
	}, nil
}

func (s *Service) RevealSecretResult(projectID, sessionID, secretID string) (SecretResultReveal, error) {
	projectID = strings.TrimSpace(projectID)
	sessionID = strings.TrimSpace(sessionID)
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return SecretResultReveal{}, fmt.Errorf("secret result ID cannot be empty")
	}
	if projectID == "" || sessionID == "" {
		activeProjectID, activeSessionID := s.ActiveSessionIDs()
		if projectID == "" {
			projectID = activeProjectID
		}
		if sessionID == "" {
			sessionID = activeSessionID
		}
	}
	encoded, err := s.secretVault.Get(secretResultServiceName, secretID)
	if err != nil {
		return SecretResultReveal{}, fmt.Errorf("secret result is unavailable: %w", err)
	}
	var record storedSecretResult
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return SecretResultReveal{}, fmt.Errorf("decode secret result: %w", err)
	}
	if record.ID != secretID || record.ProjectID != projectID || record.SessionID != sessionID {
		return SecretResultReveal{}, fmt.Errorf("secret result does not belong to this conversation")
	}
	return SecretResultReveal{
		ID:        record.ID,
		Label:     record.Label,
		Source:    record.Source,
		Value:     record.Value,
		CreatedAt: record.CreatedAt,
	}, nil
}

func newSecretResultID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create secret result ID: %w", err)
	}
	return "secret-" + hex.EncodeToString(buffer), nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
