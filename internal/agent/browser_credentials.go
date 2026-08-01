package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/vault"
)

const browserCredentialServiceName = "echocode.browser.credential"

func (s *Service) SaveBrowserCredential(credentialID, rawOrigin, username, password string) (WorkbenchState, error) {
	release, activityErr := s.beginActivity("saving browser credentials")
	if activityErr != nil {
		return s.WorkbenchState(), activityErr
	}
	defer release()
	origin, err := normalizeCredentialOrigin(rawOrigin)
	if err != nil {
		return s.workbenchStateLocked(), err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return s.workbenchStateLocked(), fmt.Errorf("用户名不能为空")
	}
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		credentialID = browserCredentialID(origin, username)
	}
	if strings.TrimSpace(password) == "" {
		if _, getErr := s.secretVault.Get(browserCredentialServiceName, credentialID); getErr != nil {
			return s.workbenchStateLocked(), fmt.Errorf("密码不能为空")
		}
	} else if err := s.secretVault.Set(browserCredentialServiceName, credentialID, password); err != nil {
		return s.workbenchStateLocked(), fmt.Errorf("保存网站密码失败: %w", err)
	}

	settings := s.runtimeSettings.Normalized()
	credential := BrowserCredential{
		ID:                 credentialID,
		Origin:             origin,
		Username:           username,
		PasswordConfigured: true,
	}
	replaced := false
	for index, existing := range settings.Browser.Credentials {
		if existing.ID == credentialID || (strings.EqualFold(existing.Origin, origin) && existing.Username == username) {
			settings.Browser.Credentials[index] = credential
			replaced = true
			break
		}
	}
	if !replaced {
		settings.Browser.Credentials = append(settings.Browser.Credentials, credential)
	}
	settings.Browser.PasswordManagerEnabled = true
	s.runtimeSettings = settings.Normalized()
	if err := saveRuntimeSettings(s.settingsPath, s.runtimeSettings); err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

func (s *Service) DeleteBrowserCredential(credentialID string) (WorkbenchState, error) {
	release, err := s.beginActivity("deleting browser credentials")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return s.workbenchStateLocked(), fmt.Errorf("凭据 ID 不能为空")
	}
	if err := s.secretVault.Delete(browserCredentialServiceName, credentialID); err != nil {
		return s.workbenchStateLocked(), fmt.Errorf("删除网站密码失败: %w", err)
	}
	settings := s.runtimeSettings.Normalized()
	filtered := settings.Browser.Credentials[:0]
	for _, credential := range settings.Browser.Credentials {
		if credential.ID != credentialID {
			filtered = append(filtered, credential)
		}
	}
	settings.Browser.Credentials = filtered
	s.runtimeSettings = settings.Normalized()
	if err := saveRuntimeSettings(s.settingsPath, s.runtimeSettings); err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

func (s *Service) BrowserCredentialSecret(credentialID string) (BrowserCredential, string, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	credentialID = strings.TrimSpace(credentialID)
	settings := s.runtimeSettings.Normalized()
	for _, credential := range settings.Browser.Credentials {
		if credential.ID != credentialID {
			continue
		}
		password, err := s.secretVault.Get(browserCredentialServiceName, credentialID)
		if errors.Is(err, vault.ErrSecretNotFound) {
			return credential, "", fmt.Errorf("网站密码已不存在，请重新保存")
		}
		if err != nil {
			return credential, "", err
		}
		return credential, password, nil
	}
	return BrowserCredential{}, "", fmt.Errorf("网站凭据不存在")
}

func normalizeCredentialOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("网站地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("网站地址必须是有效的 HTTP/HTTPS 来源")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func browserCredentialID(origin, username string) string {
	hash := sha256.Sum256([]byte(origin + "\x00" + username))
	return "browser-" + hex.EncodeToString(hash[:8])
}
