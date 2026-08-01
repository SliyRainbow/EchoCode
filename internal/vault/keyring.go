package vault

import "errors"

var ErrSecretNotFound = errors.New("secret not found")

type Vault interface {
	Set(service string, account string, secret string) error
	Get(service string, account string) (string, error)
	Delete(service string, account string) error
}

type MemoryVault struct {
	secrets map[string]string
}

func NewMemoryVault() *MemoryVault {
	return &MemoryVault{secrets: map[string]string{}}
}

func (v *MemoryVault) Set(service string, account string, secret string) error {
	v.secrets[service+":"+account] = secret
	return nil
}

func (v *MemoryVault) Get(service string, account string) (string, error) {
	secret, ok := v.secrets[service+":"+account]
	if !ok {
		return "", ErrSecretNotFound
	}
	return secret, nil
}

func (v *MemoryVault) Delete(service string, account string) error {
	delete(v.secrets, service+":"+account)
	return nil
}
