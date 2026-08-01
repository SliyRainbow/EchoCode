//go:build windows

package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDPAPIFileVaultRoundTripAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.dpapi.json")
	first := newDPAPIFileVault(path)
	const service = "EchoCode-test"
	const account = "provider:test"
	const secret = "sk-secret-value-123"

	if err := first.Set(service, account, secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatal("DPAPI vault contains the plaintext secret")
	}

	second := newDPAPIFileVault(path)
	got, err := second.Get(service, account)
	if err != nil {
		t.Fatalf("Get from a new instance: %v", err)
	}
	if got != secret {
		t.Fatalf("Get = %q, want %q", got, secret)
	}
	if err := second.Delete(service, account); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := first.Get(service, account); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrSecretNotFound", err)
	}
}

func TestResilientWindowsVaultUsesAndHealsFallback(t *testing.T) {
	primary := &toggleVault{MemoryVault: NewMemoryVault(), fail: true}
	fallback := NewMemoryVault()
	v := newResilientWindowsVault(primary, fallback)

	if err := v.Set("service", "account", "secret"); err != nil {
		t.Fatalf("Set with unavailable primary: %v", err)
	}
	got, err := v.Get("service", "account")
	if err != nil || got != "secret" {
		t.Fatalf("fallback Get = %q, %v", got, err)
	}

	primary.fail = false
	got, err = v.Get("service", "account")
	if err != nil || got != "secret" {
		t.Fatalf("healing Get = %q, %v", got, err)
	}
	healed, err := primary.MemoryVault.Get("service", "account")
	if err != nil || healed != "secret" {
		t.Fatalf("primary was not healed: %q, %v", healed, err)
	}
}

type toggleVault struct {
	*MemoryVault
	fail bool
}

func (v *toggleVault) Set(service, account, secret string) error {
	if v.fail {
		return errors.New("vault unavailable")
	}
	return v.MemoryVault.Set(service, account, secret)
}

func (v *toggleVault) Get(service, account string) (string, error) {
	if v.fail {
		return "", errors.New("vault unavailable")
	}
	return v.MemoryVault.Get(service, account)
}

func (v *toggleVault) Delete(service, account string) error {
	if v.fail {
		return errors.New("vault unavailable")
	}
	return v.MemoryVault.Delete(service, account)
}
