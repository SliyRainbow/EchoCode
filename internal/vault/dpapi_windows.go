//go:build windows

package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const dpapiVaultVersion = 1

var dpapiEntropy = []byte("EchoCode/vault/v1")

type dpapiFileVault struct {
	path string
	mu   sync.Mutex
}

type dpapiVaultFile struct {
	Version int               `json:"version"`
	Secrets map[string]string `json:"secrets"`
}

func newDPAPIFileVault(path string) Vault {
	return &dpapiFileVault{path: filepath.Clean(path)}
}

func (v *dpapiFileVault) Set(service, account, secret string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, err := v.load()
	if err != nil {
		return err
	}
	protected, err := dpapiProtect([]byte(secret))
	if err != nil {
		return fmt.Errorf("protect secret with DPAPI: %w", err)
	}
	state.Secrets[dpapiEntryKey(service, account)] = base64.StdEncoding.EncodeToString(protected)
	return v.save(state)
}

func (v *dpapiFileVault) Get(service, account string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, err := v.load()
	if err != nil {
		return "", err
	}
	encoded, ok := state.Secrets[dpapiEntryKey(service, account)]
	if !ok {
		return "", ErrSecretNotFound
	}
	protected, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode DPAPI secret: %w", err)
	}
	plain, err := dpapiUnprotect(protected)
	if err != nil {
		return "", fmt.Errorf("unprotect secret with DPAPI: %w", err)
	}
	return string(plain), nil
}

func (v *dpapiFileVault) Delete(service, account string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, err := v.load()
	if err != nil {
		return err
	}
	key := dpapiEntryKey(service, account)
	if _, ok := state.Secrets[key]; !ok {
		return nil
	}
	delete(state.Secrets, key)
	return v.save(state)
}

func (v *dpapiFileVault) load() (dpapiVaultFile, error) {
	state := dpapiVaultFile{Version: dpapiVaultVersion, Secrets: map[string]string{}}
	payload, err := os.ReadFile(v.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read DPAPI vault: %w", err)
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return state, fmt.Errorf("decode DPAPI vault: %w", err)
	}
	if state.Version != dpapiVaultVersion {
		return state, fmt.Errorf("unsupported DPAPI vault version %d", state.Version)
	}
	if state.Secrets == nil {
		state.Secrets = map[string]string{}
	}
	return state, nil
}

func (v *dpapiFileVault) save(state dpapiVaultFile) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode DPAPI vault: %w", err)
	}
	dir := filepath.Dir(v.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create DPAPI vault directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("create DPAPI vault temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure DPAPI vault temporary file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write DPAPI vault: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush DPAPI vault: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close DPAPI vault: %w", err)
	}
	if err := replaceDPAPIFile(temporaryPath, v.path); err != nil {
		return fmt.Errorf("replace DPAPI vault: %w", err)
	}
	keepTemporary = false
	return nil
}

func replaceDPAPIFile(source, destination string) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePtr,
		destinationPtr,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func dpapiEntryKey(service, account string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(service + "\x00" + account))
}

func dpapiProtect(plain []byte) ([]byte, error) {
	input := dataBlob(plain)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input,
		nil,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, err
	}
	return copyAndFreeDataBlob(output), nil
}

func dpapiUnprotect(protected []byte) ([]byte, error) {
	input := dataBlob(protected)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(
		&input,
		nil,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, err
	}
	return copyAndFreeDataBlob(output), nil
}

func dataBlob(value []byte) windows.DataBlob {
	blob := windows.DataBlob{Size: uint32(len(value))}
	if len(value) > 0 {
		blob.Data = &value[0]
	}
	return blob
}

func copyAndFreeDataBlob(blob windows.DataBlob) []byte {
	if blob.Data == nil {
		return nil
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(blob.Data))))
	return append([]byte(nil), unsafe.Slice(blob.Data, blob.Size)...)
}

type resilientWindowsVault struct {
	primary  Vault
	fallback Vault
}

func newResilientWindowsVault(primary, fallback Vault) Vault {
	return &resilientWindowsVault{primary: primary, fallback: fallback}
}

func (v *resilientWindowsVault) Set(service, account, secret string) error {
	primaryErr := v.primary.Set(service, account, secret)
	fallbackErr := v.fallback.Set(service, account, secret)
	if primaryErr != nil && fallbackErr != nil {
		return fmt.Errorf("save secret to Windows vaults: %w", errors.Join(primaryErr, fallbackErr))
	}
	return nil
}

func (v *resilientWindowsVault) Get(service, account string) (string, error) {
	secret, primaryErr := v.primary.Get(service, account)
	if primaryErr == nil {
		_ = v.fallback.Set(service, account, secret)
		return secret, nil
	}
	secret, fallbackErr := v.fallback.Get(service, account)
	if fallbackErr == nil {
		_ = v.primary.Set(service, account, secret)
		return secret, nil
	}
	if errors.Is(primaryErr, ErrSecretNotFound) && errors.Is(fallbackErr, ErrSecretNotFound) {
		return "", ErrSecretNotFound
	}
	return "", fmt.Errorf("read secret from Windows vaults: %w", errors.Join(primaryErr, fallbackErr))
}

func (v *resilientWindowsVault) Delete(service, account string) error {
	primaryErr := v.primary.Delete(service, account)
	fallbackErr := v.fallback.Delete(service, account)
	if primaryErr != nil || fallbackErr != nil {
		return fmt.Errorf("delete secret from Windows vaults: %w", errors.Join(primaryErr, fallbackErr))
	}
	return nil
}
