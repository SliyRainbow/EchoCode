//go:build windows

package vault

import "testing"

func TestWinCredVaultRoundTrip(t *testing.T) {
	v := NewWinCredVault()
	const service = "EchoCode-test"
	const account = "unit-test-account"
	const secret = "sk-测试密钥-中文-12345"

	// 清理可能的残留。
	_ = v.Delete(service, account)

	// 写入。
	if err := v.Set(service, account, secret); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	t.Cleanup(func() { _ = v.Delete(service, account) })

	// 读回并校验（含中文，验证 UTF-8 往返）。
	got, err := v.Get(service, account)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got != secret {
		t.Fatalf("读回不一致: %q, want %q", got, secret)
	}

	// 覆盖写。
	const secret2 = "sk-new-value"
	if err := v.Set(service, account, secret2); err != nil {
		t.Fatalf("覆盖写失败: %v", err)
	}
	got2, _ := v.Get(service, account)
	if got2 != secret2 {
		t.Fatalf("覆盖后读回 = %q, want %q", got2, secret2)
	}

	// 删除后应报 not found。
	if err := v.Delete(service, account); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := v.Get(service, account); err != ErrSecretNotFound {
		t.Fatalf("删除后 Get 应返回 ErrSecretNotFound, got %v", err)
	}
}

func TestWinCredVaultGetMissing(t *testing.T) {
	v := NewWinCredVault()
	if _, err := v.Get("EchoCode-test", "definitely-does-not-exist-xyz"); err != ErrSecretNotFound {
		t.Fatalf("不存在的密钥应返回 ErrSecretNotFound, got %v", err)
	}
}
