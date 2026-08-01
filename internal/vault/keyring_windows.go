//go:build windows

package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinCredVault 用 Windows 凭据管理器（Credential Manager）持久化密钥。
// 直接 syscall advapi32.dll 的 CredWriteW/CredReadW/CredDeleteW，零第三方依赖。
// 密钥由操作系统按当前用户加密存储，不落明文文件。
type WinCredVault struct{}

func NewWinCredVault() *WinCredVault { return &WinCredVault{} }

const (
	credTypeGeneric      = 1 // CRED_TYPE_GENERIC
	credPersistLocalMach = 2 // CRED_PERSIST_LOCAL_MACHINE
)

var (
	modAdvapi32    = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW = modAdvapi32.NewProc("CredWriteW")
	procCredReadW  = modAdvapi32.NewProc("CredReadW")
	procCredDelete = modAdvapi32.NewProc("CredDeleteW")
	procCredFree   = modAdvapi32.NewProc("CredFree")
)

// credentialW 对应 Windows CREDENTIALW 结构体。
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// targetName 把 service+account 拼成凭据管理器里的唯一目标名。
func targetName(service, account string) string {
	return service + ":" + account
}

func (v *WinCredVault) Set(service, account, secret string) error {
	target, err := windows.UTF16PtrFromString(targetName(service, account))
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return err
	}
	blob := []byte(secret)
	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMach,
		UserName:           user,
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if ret == 0 {
		return wrapCredError("CredWrite", callErr)
	}
	return nil
}

func (v *WinCredVault) Get(service, account string) (string, error) {
	target, err := windows.UTF16PtrFromString(targetName(service, account))
	if err != nil {
		return "", err
	}
	var pcred *credentialW
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pcred)),
	)
	if ret == 0 {
		if isNotFound(callErr) {
			return "", ErrSecretNotFound
		}
		return "", wrapCredError("CredRead", callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pcred)))

	if pcred.CredentialBlobSize == 0 || pcred.CredentialBlob == nil {
		return "", nil
	}
	blob := unsafe.Slice(pcred.CredentialBlob, pcred.CredentialBlobSize)
	return string(blob), nil
}

func (v *WinCredVault) Delete(service, account string) error {
	target, err := windows.UTF16PtrFromString(targetName(service, account))
	if err != nil {
		return err
	}
	ret, _, callErr := procCredDelete.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
	)
	if ret == 0 {
		if isNotFound(callErr) {
			return nil // 已不存在视为删除成功
		}
		return wrapCredError("CredDelete", callErr)
	}
	return nil
}

// ErrorNotFound 对应 Windows 错误码 1168（ERROR_NOT_FOUND）。
const errorNotFound = syscall.Errno(1168)

func isNotFound(err error) bool {
	return errors.Is(err, errorNotFound)
}

func wrapCredError(op string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return errors.New(op + " 失败")
	}
	return errors.New(op + " 失败: " + err.Error())
}

// NewOSVault 返回当前平台的默认持久化 Vault（Windows 用凭据管理器）。
func NewOSVault() Vault {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return NewWinCredVault()
	}
	return newResilientWindowsVault(
		NewWinCredVault(),
		newDPAPIFileVault(filepath.Join(configDir, "EchoCode", "secrets.dpapi.json")),
	)
}
