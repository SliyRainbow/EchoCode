package agent

import (
	"regexp"
	"strings"
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?im)(密码|口令|passwd|password|pwd|api[ _-]?key|apikey|access[ _-]?token|refresh[ _-]?token|token|secret|密钥)(\s*[:=：]\s*)([^\s,，;；]+)`)
	bearerTokenPattern      = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]{10,}=*`)
	apiKeyPattern           = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{10,}|AIza[A-Za-z0-9_-]{20,})\b`)
	urlPasswordPattern      = regexp.MustCompile(`(?i)(https?://[^\s/:@]+:)([^\s/@]+)(@)`)
	privateKeyPattern       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

// redactSensitiveText protects durable logs and diagnostics. A scoped
// credential reference is safe to persist because the secret itself remains in
// the OS credential store.
func redactSensitiveText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	redacted := privateKeyPattern.ReplaceAllString(value, "[已隐藏私钥]")
	redacted = secretAssignmentPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		indexes := secretAssignmentPattern.FindStringSubmatchIndex(match)
		if len(indexes) >= 8 {
			secret := match[indexes[6]:indexes[7]]
			if strings.HasPrefix(secret, scopedCredentialScheme) {
				return match
			}
			return match[:indexes[6]] + "[已隐藏]"
		}
		return "[已隐藏]"
	})
	redacted = bearerTokenPattern.ReplaceAllString(redacted, `${1}[已隐藏]`)
	redacted = apiKeyPattern.ReplaceAllString(redacted, "[已隐藏密钥]")
	redacted = urlPasswordPattern.ReplaceAllString(redacted, `${1}[已隐藏]${3}`)
	return redacted
}
