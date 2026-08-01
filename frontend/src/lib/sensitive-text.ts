const secretAssignmentPattern = /((?:密码|口令|passwd|password|pwd|api[ _-]?key|apikey|access[ _-]?token|refresh[ _-]?token|token|secret|密钥)\s*[:=：]\s*)([^\s,，;；]+)/gi;
const bearerTokenPattern = /(bearer\s+)[A-Za-z0-9._~+/-]{10,}=*/gi;
const apiKeyPattern = /\b(?:sk-[A-Za-z0-9_-]{10,}|AIza[A-Za-z0-9_-]{20,})\b/g;
const urlPasswordPattern = /(https?:\/\/[^\s/:@]+:)([^\s/@]+)(@)/gi;
const privateKeyPattern = /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g;

export function redactSensitiveTextForDisplay(value: string): string {
  if (!value.trim()) return value;
  return value
    .replace(privateKeyPattern, "[已隐藏私钥]")
    .replace(secretAssignmentPattern, (match, prefix: string, secret: string) => (
      secret.startsWith("echocode-credential://") ? match : `${prefix}[已隐藏，发送后托管]`
    ))
    .replace(bearerTokenPattern, "$1[已隐藏]")
    .replace(apiKeyPattern, "[已隐藏密钥]")
    .replace(urlPasswordPattern, "$1[已隐藏]$3");
}
