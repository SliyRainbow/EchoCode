package agent

import (
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestRedactSensitiveTextMasksDurableSecrets(t *testing.T) {
	input := "IP: 192.0.2.10\n用户名: root\n密码：temporary-password\nAPI Key=sk-exampletemporary12345\nAuthorization: Bearer abcdefghijklmnop"
	redacted := redactSensitiveText(input)
	for _, secret := range []string{"temporary-password", "sk-exampletemporary12345", "abcdefghijklmnop"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q survived redaction: %q", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "192.0.2.10") || !strings.Contains(redacted, "root") {
		t.Fatalf("non-secret connection context was removed: %q", redacted)
	}
}

func TestPrepareScopedUserPromptStoresSSHSecretAsOpaqueReference(t *testing.T) {
	secrets := vault.NewMemoryVault()
	config := ServiceConfig{
		SkillsDir:   t.TempDir(),
		SessionsDir: t.TempDir(),
		Vault:       secrets,
	}
	service := NewService(config)
	defer service.Close()

	const password = "temporary-password-value"
	prepared, err := service.prepareScopedUserPrompt("IP: 192.0.2.10用户名：root\n密码：" + password + "\n请部署网站")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prepared, password) {
		t.Fatalf("provider prompt still contains the password: %q", prepared)
	}
	referenceIndex := strings.Index(prepared, scopedCredentialScheme)
	if referenceIndex < 0 {
		t.Fatalf("provider prompt has no credential reference: %q", prepared)
	}
	referenceTail := prepared[referenceIndex+len(scopedCredentialScheme):]
	credentialID := strings.Fields(referenceTail)[0]
	credential, err := service.resolveScopedSSHCredential(credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Host != "192.0.2.10" || credential.Port != 22 || credential.Username != "root" || credential.Password != password {
		t.Fatalf("stored credential = %#v", credential)
	}
	if redacted := redactSensitiveText(prepared); redacted != prepared {
		t.Fatalf("opaque reference was removed by durable redaction:\n got: %q\nwant: %q", redacted, prepared)
	}

	service.recordUserEvent(prepared)
	reloaded := NewService(config)
	defer reloaded.Close()
	history := reloaded.GetSessionMessages()
	if len(history) != 1 || strings.Contains(history[0].Content, password) || !strings.Contains(history[0].Content, scopedCredentialScheme+credentialID) {
		t.Fatalf("reloaded history = %#v", history)
	}
	if _, err := reloaded.resolveScopedSSHCredential(credentialID); err != nil {
		t.Fatalf("credential reference did not survive session reload: %v", err)
	}
}

func TestPrepareScopedUserPromptAcceptsBareIPv4AndRestoresCredentialContext(t *testing.T) {
	secrets := vault.NewMemoryVault()
	service := NewService(ServiceConfig{
		SkillsDir: t.TempDir(),
		Vault:     secrets,
	})
	defer service.Close()

	const password = "temporary-password-for-p"
	prepared, err := service.prepareScopedUserPrompt("P:203.0.113.10用户名：root\n密码：" + password + "\n帮我部署网站")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prepared, password) {
		t.Fatalf("password survived bare-host replacement: %q", prepared)
	}
	referenceIndex := strings.Index(prepared, scopedCredentialScheme+"ssh-")
	if referenceIndex < 0 {
		t.Fatalf("bare IPv4 input did not produce a scoped reference: %q", prepared)
	}
	reference := strings.Fields(prepared[referenceIndex:])[0]
	credential, err := service.resolveScopedSSHCredential(reference)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Host != "203.0.113.10" || credential.Port != 22 || credential.Username != "root" || credential.Password != password {
		t.Fatalf("stored credential = %#v", credential)
	}

	currentContext := service.scopedSSHContext(prepared)
	if !strings.Contains(currentContext, reference) || !strings.Contains(currentContext, "root@203.0.113.10:22") {
		t.Fatalf("current credential context = %q", currentContext)
	}
	if strings.Contains(currentContext, password) {
		t.Fatalf("credential context leaked password: %q", currentContext)
	}

	service.sessionMessages = []protocol.Message{{Role: "user", Content: prepared}}
	continuedContext := service.scopedSSHContext("继续")
	if !strings.Contains(continuedContext, reference) {
		t.Fatalf("historical credential reference was not restored: %q", continuedContext)
	}
	preview := service.contextPreviewForInput("继续")
	if len(preview.VolatileTail) == 0 || !strings.Contains(preview.VolatileTail[len(preview.VolatileTail)-1].Content, reference) {
		t.Fatalf("context preview did not expose the valid credential reference: %#v", preview.VolatileTail)
	}
}

func TestPrepareScopedUserPromptLeavesIncompleteLoginUntouched(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), Vault: vault.NewMemoryVault()})
	defer service.Close()
	input := "密码：not-an-ssh-login"
	prepared, err := service.prepareScopedUserPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != input {
		t.Fatalf("incomplete login was rewritten: %q", prepared)
	}
}

func TestTerminalTurnAndRedactedPromptSurviveSessionReload(t *testing.T) {
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()}
	service := NewService(config)
	service.recordUserEvent("connect\n密码: do-not-persist")
	if err := service.recordTurnTerminal("failed", "本轮执行失败：upstream unavailable", "test-model", nil, 1_250); err != nil {
		t.Fatal(err)
	}

	reloaded := NewService(config)
	history := reloaded.GetSessionMessages()
	if len(history) != 2 {
		t.Fatalf("history = %#v", history)
	}
	if strings.Contains(history[0].Content, "do-not-persist") || !strings.Contains(history[0].Content, "[已隐藏]") {
		t.Fatalf("prompt was persisted without redaction: %#v", history[0])
	}
	if history[1].Status != "failed" || history[1].DurationMs != 1_250 || !strings.Contains(history[1].Content, "upstream unavailable") {
		t.Fatalf("terminal state was not restored: %#v", history[1])
	}
}
