package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestBrowserCredentialUsesVault(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	service := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SettingsPath: settingsPath,
		Vault:        vault.NewMemoryVault(),
	})
	state, err := service.SaveBrowserCredential("", "example.com/login", "alice", "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.RuntimeSettings.Browser.Credentials) != 1 {
		t.Fatalf("credentials = %+v", state.RuntimeSettings.Browser.Credentials)
	}
	credential := state.RuntimeSettings.Browser.Credentials[0]
	if credential.Origin != "https://example.com" || !credential.PasswordConfigured {
		t.Fatalf("credential = %+v", credential)
	}
	stored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "secret-value") {
		t.Fatal("password must not be written to runtime-settings.json")
	}
	loaded, password, err := service.BrowserCredentialSecret(credential.ID)
	if err != nil || loaded.Username != "alice" || password != "secret-value" {
		t.Fatalf("loaded=%+v password=%q err=%v", loaded, password, err)
	}
	state, err = service.DeleteBrowserCredential(credential.ID)
	if err != nil || len(state.RuntimeSettings.Browser.Credentials) != 0 {
		t.Fatalf("delete state=%+v err=%v", state.RuntimeSettings.Browser.Credentials, err)
	}
}
