import type { MessagePart } from "../types";

export type SecretResultPart = Extract<MessagePart, { kind: "secret_result" }>;

export type SecretResultGroup = {
  key: string;
  title: string;
  source?: string;
  parts: SecretResultPart[];
};

type SecretFieldKind = "account" | "password" | "token" | "key" | "other";

export function groupSecretResults(parts: SecretResultPart[]): SecretResultGroup[] {
  const groups = new Map<string, SecretResultGroup>();
  for (const part of parts) {
    const label = part.secretLabel?.trim() || "远程敏感结果";
    const kind = secretFieldKind(label);
    const base = secretLabelBase(label);
    const family = kind === "account" || kind === "password" ? "login" : kind;
    const key = `${part.secretSource?.trim().toLocaleLowerCase() || "local"}\u0000${base.toLocaleLowerCase()}\u0000${family}`;
    const existing = groups.get(key);
    if (existing) {
      existing.parts.push(part);
      existing.title = secretGroupTitle(existing.parts, base);
      continue;
    }
    groups.set(key, {
      key,
      title: secretGroupTitle([part], base),
      source: part.secretSource?.trim(),
      parts: [part],
    });
  }
  return [...groups.values()].map((group) => ({
    ...group,
    parts: [...group.parts].sort((left, right) => secretFieldOrder(left) - secretFieldOrder(right)),
  }));
}

export function secretResultFieldLabel(part: SecretResultPart): string {
  switch (secretFieldKind(part.secretLabel || "")) {
    case "account": return "账号";
    case "password": return "密码";
    case "token": return "令牌";
    case "key": return "密钥";
    default: return part.secretLabel?.trim() || "内容";
  }
}

function secretGroupTitle(parts: SecretResultPart[], base: string): string {
  if (parts.length === 1) return parts[0].secretLabel?.trim() || "远程敏感结果";
  const kinds = new Set(parts.map((part) => secretFieldKind(part.secretLabel || "")));
  if ([...kinds].every((kind) => kind === "account" || kind === "password")) {
    return `${base || ""}登录凭据`;
  }
  return `${base || ""}受保护结果`;
}

function secretLabelBase(label: string): string {
  return label
    .trim()
    .replace(/\s*(?:登录)?(?:账号|账户|用户名|用户|密码|口令|令牌|token|密钥|api\s*key)\s*$/i, "")
    .trim();
}

function secretFieldKind(label: string): SecretFieldKind {
  const normalized = label.trim();
  if (/(?:账号|账户|用户名|用户)\s*$/i.test(normalized)) return "account";
  if (/(?:密码|口令)\s*$/i.test(normalized)) return "password";
  if (/(?:令牌|token)\s*$/i.test(normalized)) return "token";
  if (/(?:密钥|api\s*key)\s*$/i.test(normalized)) return "key";
  return "other";
}

function secretFieldOrder(part: SecretResultPart): number {
  switch (secretFieldKind(part.secretLabel || "")) {
    case "account": return 0;
    case "password": return 1;
    case "token": return 2;
    case "key": return 3;
    default: return 4;
  }
}
