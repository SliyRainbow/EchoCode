const exactModelContextWindows: Record<string, number> = {
  "gpt-5": 400_000,
  "gpt-5-mini": 400_000,
  "gpt-5-nano": 400_000,
  "gpt-5-pro": 400_000,
  "gpt-5-chat-latest": 128_000,
  "gpt-5.1": 400_000,
  "gpt-5.1-chat-latest": 128_000,
  "gpt-5.1-codex": 400_000,
  "gpt-5.1-codex-mini": 400_000,
  "gpt-5.1-codex-max": 400_000,
  "gpt-5.2": 400_000,
  "gpt-5.2-2025-12-11": 400_000,
  "gpt-5.2-chat-latest": 128_000,
  "gpt-5.2-pro": 400_000,
  "gpt-5.2-pro-2025-12-11": 400_000,
  "gpt-5.2-codex": 400_000,
  "gpt-5.3-codex": 400_000,
  "gpt-5.4": 1_050_000,
  "gpt-5.4-2026-03-05": 1_050_000,
  "gpt-5.4-pro": 1_050_000,
  "gpt-5.4-mini": 400_000,
  "gpt-5.4-nano": 400_000,
  "gpt-5.5": 1_050_000,
  "gpt-5.6": 1_050_000,
  "gpt-5.6-luna": 1_050_000,
  "gpt-5.6-sol": 1_050_000,
  "gpt-5.6-terra": 1_050_000,
  "gpt-4.1": 1_047_576,
  "gpt-4.1-mini": 1_047_576,
  "gpt-4.1-nano": 1_047_576,
  "gpt-4o": 128_000,
  "gpt-4o-mini": 128_000,
  "gpt-4o-audio-preview": 128_000,
  "gpt-4o-realtime-preview": 128_000,
  "chatgpt-4o-latest": 128_000,
  "gpt-4-turbo": 128_000,
  "gpt-3.5-turbo": 16_385,
  "gpt-3.5-turbo-0125": 16_385,
  "o1": 200_000,
  "o1-pro": 200_000,
  "o1-mini": 128_000,
  "o3": 200_000,
  "o3-pro": 200_000,
  "o3-mini": 200_000,
  "o4-mini": 200_000,
  "deepseek-chat": 128_000,
  "deepseek-reasoner": 128_000,
  // xAI model cards, verified against https://docs.x.ai/developers/models on 2026-07-19.
  "grok-4.5": 500_000,
  "grok-4.5-latest": 500_000,
  "grok-build-latest": 500_000,
  "grok-4.3": 1_000_000,
  "grok-4.3-latest": 1_000_000,
  "grok-latest": 1_000_000,
  "grok-4.20-0309-reasoning": 1_000_000,
  "grok-4.20-reasoning-latest": 1_000_000,
  "grok-4.20": 1_000_000,
  "grok-4.20-reasoning": 1_000_000,
  "grok-4.20-0309": 1_000_000,
  "grok-4.20-beta-0309-reasoning": 1_000_000,
  "grok-4.20-beta": 1_000_000,
  "grok-4.20-beta-0309": 1_000_000,
  "grok-4.20-beta-latest": 1_000_000,
  "grok-4.20-beta-latest-reasoning": 1_000_000,
  "grok-4.20-beta-reasoning": 1_000_000,
  "grok-4.20-experimental-beta-0304-reasoning": 1_000_000,
  "grok-4.20-experimental-beta-0304": 1_000_000,
  "grok-4.20-experimental-beta-reasoning-latest": 1_000_000,
  "grok-4.20-experimental-beta-latest": 1_000_000,
  "grok-4.20-reasoning-gv2": 1_000_000,
  "grok-4.20-0309-non-reasoning": 1_000_000,
  "grok-4.20-non-reasoning": 1_000_000,
  "grok-4.20-non-reasoning-latest": 1_000_000,
  "grok-4.20-beta-non-reasoning": 1_000_000,
  "grok-4.20-beta-latest-non-reasoning": 1_000_000,
  "grok-4.20-experimental-beta-0304-non-reasoning": 1_000_000,
  "grok-4.20-experimental-beta-non-reasoning-latest": 1_000_000,
  "grok-4.20-beta-0309-non-reasoning": 1_000_000,
  "grok-4.20-non-reasoning-gv2": 1_000_000,
  "grok-4.20-multi-agent-0309": 1_000_000,
  "grok-4.20-multi-agent": 1_000_000,
  "grok-4.20-multi-agent-latest": 1_000_000,
  "grok-4.20-multi-agent-beta-latest": 1_000_000,
  "grok-4.20-multi-agent-experimental-beta-0304": 1_000_000,
  "grok-4.20-multi-agent-experimental-beta-latest": 1_000_000,
  "grok-4.20-multi-agent-beta-0309": 1_000_000,
  "grok-build-0.1": 256_000,
  "grok-code-fast-1": 256_000,
  "grok-code-fast": 256_000,
  "grok-code-fast-1-0825": 256_000,
};

const anthropicModelContextWindows: Record<string, number> = {
  "claude-haiku-4-5": 200_000,
  "claude-opus-4-5": 200_000,
  "claude-sonnet-4-5": 200_000,
  "claude-opus-4-1": 200_000,
  "claude-opus-4": 200_000,
  "claude-sonnet-4": 200_000,
  "claude-3-7-sonnet": 200_000,
};

function matchesAnthropicAlias(modelID: string, alias: string) {
  if (modelID === alias || modelID === `${alias}-latest`) return true;
  const suffix = modelID.startsWith(`${alias}-`) ? modelID.slice(alias.length + 1) : "";
  return /^\d{8}$/.test(suffix);
}

function anthropicCatalogContextWindow(modelID: string) {
  const normalizedID = modelID.startsWith("anthropic/") ? modelID.slice("anthropic/".length) : modelID;
  for (const [alias, tokens] of Object.entries(anthropicModelContextWindows)) {
    if (matchesAnthropicAlias(normalizedID, alias)) return tokens;
  }
  return 0;
}

export function inferModelContextWindow(modelID: string, protocol: string, providerDefault = 0) {
  const id = modelID.trim().toLowerCase();
  const catalogTokens = exactModelContextWindows[id] ?? anthropicCatalogContextWindow(id);
  if (catalogTokens > 0) {
    return { tokens: catalogTokens, source: "catalog" };
  }
  if (providerDefault > 0) {
    return { tokens: providerDefault, source: "provider-default" };
  }
  if (protocol === "deepseek-official") return { tokens: 128_000, source: "protocol-default" };
  if (protocol === "gemini") return { tokens: 1_048_576, source: "protocol-default" };
  return { tokens: 64 * 1024, source: "safe-default" };
}

export function contextWindowSourceLabel(source?: string) {
  switch (source) {
    case "upstream": return "上游返回";
    case "catalog": return "模型目录";
    case "protocol-default": return "协议默认";
    case "provider-default": return "供应商默认";
    case "manual": return "手动设置";
    case "safe-default": return "安全估算";
    default: return "待识别";
  }
}
