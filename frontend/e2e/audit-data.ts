// Representative UI data only. No commands, providers or plugins are executed.
export function withAuditData(seed: any) {
  const state = seed.state;
  const server = state.runtimeSettings.mcp.servers[0];
  state.runtimeSettings.mcp.servers.push(
    { ...structuredClone(server), id: "repository-analysis", name: "仓库分析与文档检索", transport: "stdio", command: "node", args: ["C:/fixture/tools/repository-server.mjs"], enabled: true },
    { ...structuredClone(server), id: "remote-docs", name: "远程文档服务 · 开发环境", transport: "streamable-http", url: "https://docs.example.test/mcp", enabled: false },
  );
  state.mcpServers.push(
    { id: "repository-analysis", name: "仓库分析与文档检索", transport: "stdio", state: "ready", message: "已连接，可以读取项目中经过授权的代码和文档。", toolCount: 12 },
    { id: "remote-docs", name: "远程文档服务 · 开发环境", transport: "streamable-http", state: "error", message: "连接未完成，请检查远程服务地址及访问权限。", toolCount: 0 },
  );
  state.plugins.push({
    ...structuredClone(state.plugins[0]), id: "repository-quality", name: "仓库质量检查", version: "1.2.0", source: "installed", state: "disabled", canUninstall: true,
    description: "检查项目约定、代码格式与文档链接。", author: "Fixture", message: "启用并保存设置后可使用插件工具。",
    permissions: { fileRead: true, fileWrite: true, network: false }, grantedPermissions: { fileRead: true, fileWrite: false, network: false },
  });
  state.plugins.forEach((plugin: any) => {
    plugin.tools = [
      { name: "inspect_project", fullName: `plugin__${plugin.id}__inspect_project`, description: "检查工作区的结构与配置，不修改项目文件。", readOnly: true, permissions: { fileRead: true, fileWrite: false, network: false } },
      { name: "write_report", fullName: `plugin__${plugin.id}__write_report`, description: "在用户授权的目录中生成检查报告。", readOnly: false, permissions: { fileRead: true, fileWrite: true, network: false } },
    ];
    plugin.toolCount = 2;
    plugin.availableToolCount = plugin.state === "ready" ? 2 : 0;
  });
  state.runtimeSettings.plugins.entries.push({ id: "repository-quality", enabled: false, permissions: { fileRead: true, fileWrite: false, network: false } });
  state.usageLedger = { ...state.usageLedger, enabled: true, totalSamples: 42, totalInputTokens: 1200000, totalOutputTokens: 60000, totalEffectiveCost: 4.8, sessionSamples: 8, sessionInputTokens: 90000, sessionOutputTokens: 7000, sessionEffectiveCost: 0.42, lastRecordedAt: "2026-10-09T08:30:00Z" };
  state.usageMetrics = { inputTokens: 10000, outputTokens: 700, promptCacheHitTokens: 9000, promptCacheMissTokens: 1000, effectiveCost: 0.04 };
  state.cacheHitRate = 0.9;
  state.deepSeekSession = { ...state.deepSeekSession, active: true, providerName: "DeepSeek 官方", model: "deepseek-v4-flash", sessionCacheHitRate: 0.8, sessionCacheHitTokens: 60000, sessionCacheMissTokens: 15000, stablePromptTokens: 2100 };
  state.cacheHealth = { ...state.cacheHealth, status: "watch", hitRate: 0.9, hitTokens: 9000, missTokens: 1000, totalCacheTokens: 10000, sampleCount: 42 };
  const officialProvider = state.runtimeSettings.model.providers.find((provider: any) => provider.protocol === "openai-compatible" && provider.id !== "deepseek");
  if (officialProvider) { officialProvider.id = "audit-openai"; officialProvider.name = "OpenAI 官方（测试）"; officialProvider.baseUrl = "https://api.openai.com/v1"; officialProvider.billingApiKeyConfigured = true; }
  seed.audit = true;
  return seed;
}
