//go:build !windows

package vault

// 非 Windows 平台暂无原生凭据库实现，回退到内存 Vault（保证可编译）。
// 后续如需 mac/Linux 持久化，可在此接 Keychain / Secret Service。

// NewOSVault 返回当前平台的默认 Vault。
func NewOSVault() Vault {
	return NewMemoryVault()
}
