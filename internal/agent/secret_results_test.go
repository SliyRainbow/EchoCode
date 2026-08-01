package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestSecretResultStoresOnlyOpaqueReferenceInConversation(t *testing.T) {
	const secretValue = "target-system-secret-value"
	sessionsDir := t.TempDir()
	secretVault := vault.NewMemoryVault()
	service := NewService(ServiceConfig{
		SkillsDir:   t.TempDir(),
		SessionsDir: sessionsDir,
		Vault:       secretVault,
	})
	defer service.Close()

	part, err := service.storeSecretResult("管理员密码", "ssh://root@example.test", secretValue)
	if err != nil {
		t.Fatal(err)
	}
	if part.Kind != tools.PartSecretResult || part.SecretID == "" {
		t.Fatalf("secret result part = %#v", part)
	}
	encodedPart, err := json.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedPart), secretValue) {
		t.Fatalf("result part leaked plaintext: %s", encodedPart)
	}

	service.recordAssistantAndCheckpoint("敏感结果已安全保存。", "test-model", []tools.ResultPart{part})
	var eventBytes []byte
	err = filepath.WalkDir(sessionsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		eventBytes = append(eventBytes, data...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(eventBytes), secretValue) {
		t.Fatalf("event log leaked plaintext: %s", eventBytes)
	}
	if !strings.Contains(string(eventBytes), part.SecretID) {
		t.Fatalf("event log did not preserve opaque result ID %q", part.SecretID)
	}

	projectID, sessionID := service.ActiveSessionIDs()
	revealed, err := service.RevealSecretResult(projectID, sessionID, part.SecretID)
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Value != secretValue {
		t.Fatalf("revealed value = %q", revealed.Value)
	}
	if _, err := service.RevealSecretResult(projectID, "another-session", part.SecretID); err == nil {
		t.Fatal("cross-session reveal unexpectedly succeeded")
	}
	if _, err := service.RevealSecretResult("another-project", sessionID, part.SecretID); err == nil {
		t.Fatal("cross-project reveal unexpectedly succeeded")
	}
}

func TestSecretResultRejectsEmptyInvalidAndOversizedValues(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), Vault: vault.NewMemoryVault()})
	defer service.Close()

	for name, value := range map[string]string{
		"empty":     "  ",
		"invalid":   string([]byte{0xff, 0xfe}),
		"oversized": strings.Repeat("x", maxSecretResultBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.storeSecretResult("test", "ssh://test", value); err == nil {
				t.Fatalf("storeSecretResult accepted %s value", name)
			}
		})
	}
}

func TestSecretResultMergesDeduplicateOnlyMatchingIDs(t *testing.T) {
	account := tools.ResultPart{
		Kind:         tools.PartSecretResult,
		Status:       "ok",
		SecretID:     "secret-account",
		SecretLabel:  "管理员账号",
		SecretSource: "ssh://root@example.test",
	}
	password := tools.ResultPart{
		Kind:         tools.PartSecretResult,
		Status:       "ok",
		SecretID:     "secret-password",
		SecretLabel:  "管理员密码",
		SecretSource: "ssh://root@example.test",
	}
	accountDuplicate := tools.ResultPart{
		Kind:     tools.PartSecretResult,
		SecretID: account.SecretID,
	}

	for name, merge := range map[string]func([]tools.ResultPart, []tools.ResultPart) []tools.ResultPart{
		"outcome": mergeOutcomeParts,
		"runtime": mergeTaskRuntimeParts,
	} {
		t.Run(name, func(t *testing.T) {
			parts := merge(nil, []tools.ResultPart{account, password})
			parts = merge(parts, []tools.ResultPart{accountDuplicate})
			if len(parts) != 2 {
				t.Fatalf("protected result count = %d, want 2: %#v", len(parts), parts)
			}
			if parts[0].SecretID != account.SecretID || parts[0].SecretLabel != account.SecretLabel || parts[0].SecretSource != account.SecretSource {
				t.Fatalf("account result was duplicated or lost metadata: %#v", parts[0])
			}
			if parts[1].SecretID != password.SecretID || parts[1].SecretLabel != password.SecretLabel {
				t.Fatalf("password result was not preserved independently: %#v", parts[1])
			}
		})
	}
}
