package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	scopedCredentialServiceName = "echocode.scoped.credential"
	scopedCredentialScheme      = "echocode-credential://"
)

var (
	sshHostAssignmentPattern = regexp.MustCompile(`(?im)(?:\b(?:ip|host|hostname|server)\b|服务器(?:\s*ip)?|主机)\s*[:=：]\s*(\[[0-9a-f:]+\]|[a-z0-9.-]+)(?::(\d{1,5}))?`)
	// Accept a bare IPv4 followed by a username label as a recovery path for
	// composer input where the leading "IP" label was accidentally omitted.
	sshBareIPv4Pattern               = regexp.MustCompile(`(?im)(?:^|[^a-z0-9_.-])((?:\d{1,3}\.){3}\d{1,3})(?::(\d{1,5}))?\s*(?:\b(?:username|user|login)\b|用户名|用户)\s*[:=：]`)
	sshUserAssignmentPattern         = regexp.MustCompile(`(?im)(?:\b(?:username|user|login)\b|用户名|用户)\s*[:=：]\s*([^\s,，;；]+)`)
	sshPassAssignmentPattern         = regexp.MustCompile(`(?im)(密码|口令|passwd|password|pwd)(\s*[:=：]\s*)([^\s,，;；]+)`)
	scopedCredentialReferencePattern = regexp.MustCompile(`echocode-credential://ssh-[a-f0-9]{16}`)
)

type scopedSSHCredential struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
	CreatedAt string `json:"createdAt"`
}

func (credential scopedSSHCredential) address() string {
	return net.JoinHostPort(credential.Host, strconv.Itoa(credential.Port))
}

func (credential scopedSSHCredential) displayTarget() string {
	return credential.Username + "@" + credential.address()
}

// prepareScopedUserPrompt turns a complete password-based SSH login supplied by
// the user into an opaque reference. The secret remains in the OS credential
// store and never enters provider requests, tool arguments, approvals, or logs.
func (s *Service) prepareScopedUserPrompt(prompt string) (string, error) {
	credential, passwordStart, passwordEnd, ok := parseScopedSSHCredential(prompt)
	if !ok {
		return prompt, nil
	}
	credential.ProjectID = strings.TrimSpace(s.projectID)
	credential.SessionID = strings.TrimSpace(s.sessionID)
	credential.ID = scopedSSHCredentialID(credential.ProjectID, credential.SessionID, credential.Host, credential.Port, credential.Username)
	credential.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	encoded, err := json.Marshal(credential)
	if err != nil {
		return "", fmt.Errorf("encode scoped SSH credential: %w", err)
	}
	if err := s.secretVault.Set(scopedCredentialServiceName, credential.ID, string(encoded)); err != nil {
		return "", fmt.Errorf("store scoped SSH credential: %w", err)
	}

	reference := scopedCredentialScheme + credential.ID
	return prompt[:passwordStart] + reference + prompt[passwordEnd:], nil
}

func parseScopedSSHCredential(prompt string) (scopedSSHCredential, int, int, bool) {
	userMatch := sshUserAssignmentPattern.FindStringSubmatch(prompt)
	passwordMatch := sshPassAssignmentPattern.FindStringSubmatchIndex(prompt)
	host, port, hostOK := parseSSHHost(prompt)
	if !hostOK || len(userMatch) < 2 || len(passwordMatch) < 8 {
		return scopedSSHCredential{}, 0, 0, false
	}

	username := strings.TrimSpace(userMatch[1])
	passwordStart, passwordEnd := passwordMatch[6], passwordMatch[7]
	password := strings.TrimSpace(prompt[passwordStart:passwordEnd])
	if strings.HasPrefix(password, scopedCredentialScheme) || !validSSHHost(host) || username == "" || password == "" {
		return scopedSSHCredential{}, 0, 0, false
	}

	return scopedSSHCredential{
		Kind:     "ssh_password",
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}, passwordStart, passwordEnd, true
}

func parseSSHHost(prompt string) (string, int, bool) {
	match := sshHostAssignmentPattern.FindStringSubmatch(prompt)
	if len(match) < 2 {
		match = sshBareIPv4Pattern.FindStringSubmatch(prompt)
	}
	if len(match) < 2 {
		return "", 0, false
	}
	host := strings.Trim(strings.TrimSpace(match[1]), "[]")
	port := 22
	if len(match) > 2 && strings.TrimSpace(match[2]) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(match[2]))
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", 0, false
		}
		port = parsed
	}
	return host, port, validSSHHost(host)
}

func validSSHHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/\\@\x00") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, current := range label {
			if (current < 'a' || current > 'z') && (current < 'A' || current > 'Z') && (current < '0' || current > '9') && current != '-' {
				return false
			}
		}
	}
	return true
}

func scopedSSHCredentialID(projectID, sessionID, host string, port int, username string) string {
	key := strings.Join([]string{
		strings.TrimSpace(projectID),
		strings.TrimSpace(sessionID),
		strings.ToLower(strings.TrimSpace(host)),
		strconv.Itoa(port),
		strings.TrimSpace(username),
	}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return "ssh-" + hex.EncodeToString(sum[:8])
}

func (s *Service) resolveScopedSSHCredential(credentialID string) (scopedSSHCredential, error) {
	credentialID = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credentialID), scopedCredentialScheme))
	if !strings.HasPrefix(credentialID, "ssh-") {
		return scopedSSHCredential{}, fmt.Errorf("invalid SSH credential reference")
	}
	encoded, err := s.secretVault.Get(scopedCredentialServiceName, credentialID)
	if err != nil {
		return scopedSSHCredential{}, fmt.Errorf("SSH credential is unavailable; ask the user to provide it again: %w", err)
	}
	var credential scopedSSHCredential
	if err := json.Unmarshal([]byte(encoded), &credential); err != nil {
		return scopedSSHCredential{}, fmt.Errorf("decode SSH credential: %w", err)
	}
	if credential.ID != credentialID || credential.Kind != "ssh_password" || !validSSHHost(credential.Host) || credential.Port < 1 || credential.Port > 65535 || credential.Username == "" || credential.Password == "" {
		return scopedSSHCredential{}, fmt.Errorf("SSH credential is invalid; ask the user to provide it again")
	}
	if credential.ProjectID != "" && strings.TrimSpace(s.projectID) != "" && credential.ProjectID != strings.TrimSpace(s.projectID) {
		return scopedSSHCredential{}, fmt.Errorf("SSH credential belongs to another project")
	}
	if credential.SessionID != "" && strings.TrimSpace(s.sessionID) != "" && credential.SessionID != strings.TrimSpace(s.sessionID) {
		return scopedSSHCredential{}, fmt.Errorf("SSH credential belongs to another conversation")
	}
	return credential, nil
}

// scopedSSHContext keeps a valid host-managed password reference discoverable
// after a compressed history or a short follow-up such as "继续". It exposes
// only opaque IDs and the non-secret target; the password stays in the vault.
func (s *Service) scopedSSHContext(userInput string) string {
	seen := map[string]bool{}
	ids := make([]string, 0, 2)
	collect := func(value string) {
		for _, reference := range scopedCredentialReferencePattern.FindAllString(value, -1) {
			id := strings.TrimPrefix(reference, scopedCredentialScheme)
			if seen[id] {
				continue
			}
			credential, err := s.resolveScopedSSHCredential(id)
			if err != nil {
				continue
			}
			seen[id] = true
			ids = append(ids, scopedCredentialScheme+id+" target="+credential.displayTarget())
		}
	}
	collect(userInput)
	for _, message := range s.sessionMessages {
		collect(message.Content)
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return "Password-based SSH credentials currently available to the ssh tool (no SSH key or external authorization entry is required):\n- " + strings.Join(ids, "\n- ") + "\nUse the credential_id value with ssh; do not use shell for password login."
}

func (s *Service) scopedSSHKnownHostsPath() string {
	settingsPath := strings.TrimSpace(s.settingsPath)
	if settingsPath == "" {
		return ""
	}
	return strings.TrimSuffix(settingsPath, filepath.Ext(settingsPath)) + ".ssh_known_hosts"
}
