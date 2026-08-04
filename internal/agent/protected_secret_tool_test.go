package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestDecodeProtectedSecretStoresPlaintextOnlyInVault(t *testing.T) {
	const plaintext = "sk-test-protected-value"
	service := NewService(ServiceConfig{
		SkillsDir:   t.TempDir(),
		SessionsDir: t.TempDir(),
		Vault:       vault.NewMemoryVault(),
	})
	defer service.Close()

	encoded := base64.StdEncoding.EncodeToString([]byte(plaintext))
	tool := DecodeProtectedSecretTool{Capture: service.storeSecretResult}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"encoding":"base64",
		"value":"`+encoded+`",
		"secret_label":"API 密钥",
		"source":"用户提供的 Base64"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Parts) != 1 || result.Parts[0].Kind != tools.PartSecretResult {
		t.Fatalf("protected result = %#v", result)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), plaintext) || strings.Contains(string(serialized), encoded) {
		t.Fatalf("protected result leaked sensitive input: %s", serialized)
	}

	projectID, sessionID := service.ActiveSessionIDs()
	revealed, err := service.RevealSecretResult(projectID, sessionID, result.Parts[0].SecretID)
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Value != plaintext {
		t.Fatalf("revealed value = %q", revealed.Value)
	}
}

func TestDecodeProtectedSecretAcceptsBase64URL(t *testing.T) {
	const plaintext = "token/url-safe?yes"
	var captured string
	tool := DecodeProtectedSecretTool{Capture: func(label, source, value string) (tools.ResultPart, error) {
		captured = value
		return tools.ResultPart{Kind: tools.PartSecretResult, SecretID: "secret-test"}, nil
	}}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(plaintext))
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"encoding":"base64url","value":"`+encoded+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || captured != plaintext {
		t.Fatalf("result=%#v captured=%q", result, captured)
	}
}

func TestDecodeProtectedSecretRejectsInvalidAndOversizedInput(t *testing.T) {
	tool := DecodeProtectedSecretTool{Capture: func(label, source, value string) (tools.ResultPart, error) {
		t.Fatal("capture should not be called")
		return tools.ResultPart{}, nil
	}}
	for name, raw := range map[string]json.RawMessage{
		"invalid":   json.RawMessage(`{"encoding":"base64","value":"***"}`),
		"oversized": json.RawMessage(`{"value":"` + strings.Repeat("A", maxProtectedSecretEncodedBytes+1) + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("invalid input result = %#v", result)
			}
		})
	}
}

func TestDecodeProtectedSecretDisplayNeverIncludesEncodedValue(t *testing.T) {
	const encoded = "c2stdGVzdC1wcm90ZWN0ZWQtdmFsdWU="
	display := toolInputForDisplay("decode_protected_secret", json.RawMessage(`{
		"encoding":"base64",
		"value":"`+encoded+`",
		"secret_label":"API 密钥"
	}`))
	if display != "已接收编码值，正在保存为安全卡片" {
		t.Fatalf("display = %q", display)
	}
	if strings.Contains(display, encoded) {
		t.Fatalf("display leaked encoded input: %q", display)
	}
}

func TestDecodeProtectedSecretIsAvailableWithoutApproval(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	service.runtimeSettings.ApprovalPolicy = "on-request"
	if _, ok := service.buildToolRegistry().Get("decode_protected_secret"); !ok {
		t.Fatal("protected secret decoder is missing from the mutable registry")
	}
	if _, ok := service.buildReadOnlyRegistry().Get("decode_protected_secret"); !ok {
		t.Fatal("protected secret decoder is missing from the read-only registry")
	}
	if service.needsApproval("decode_protected_secret", nil) {
		t.Fatal("protected secret decoding should not wait for an approval card")
	}
}
