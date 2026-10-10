import type { Page } from "@playwright/test";
import { withAuditData } from "./audit-data";
import type { SessionMessage } from "../src/types";

// Exercise the real frontend with an in-memory Wails boundary. These tests never
// contact a model, execute commands, or change the user's projects/settings.
export async function installWorkbenchFixture(page: Page, options: { audit?: boolean; motion?: boolean; history?: SessionMessage[]; timeline?: boolean; timelineAvailable?: boolean; conversationView?: "recent" | "projects" } = {}) {
  await page.emulateMedia({ reducedMotion: options.motion ? "no-preference" : "reduce" });
  await page.goto("/");
  let seed = await page.evaluate(async () => {
    const modulePath = "/src/services/workbench.ts";
    const service = await import(/* @vite-ignore */ modulePath);
    const state = await service.getWorkbenchState();
    return { state, catalog: await service.getExtensionCatalog(), billing: await service.getUsageBillingReport(state.runtimeSettings.model.selectedProviderId) };
  });
  if (options.audit) seed = withAuditData(seed);
  await page.addInitScript((initial) => {
    if (localStorage.getItem("echocode:conversation-view") === null) localStorage.setItem("echocode:conversation-view", initial.conversationView);
    const state = structuredClone(initial.state);
    const catalog = structuredClone(initial.catalog);
    const catalogControls = { error: "" };
    const uiControls = { billingError: "", saveError: "", newSessionDelay: 0, automationError: "", automationSaveError: "" };
    const automation = { tasks: [] as any[], running: true, error: "" };
    const billingReports = new Map<string, any>();
    state.activeProjectId = "project-echo";
    state.activeSessionId = "session-new";
    state.runtimeSettings.workspaceRoot = "C:\\work\\EchoCode";
    state.runtimeSettings.model.providers.forEach((provider: any) => { provider.apiKeyConfigured = true; });
    state.deepSeek.configured = true;
    const date = "2026-10-09T08:00:00.000Z";
    const projects = [
      { id: "project-echo", name: "EchoCode", workspaceRoot: "C:\\work\\EchoCode", pinned: true, isActive: true, sessions: [
        { id: "session-new", title: "新对话", updatedAt: date, createdAt: date, archived: false, isActive: true },
        { id: "session-ui", title: "整理工作台交互", updatedAt: "2026-10-09T07:00:00.000Z", createdAt: date, archived: false, isActive: false },
        { id: "session-api", title: "检查 API 错误处理", updatedAt: "2026-10-09T06:00:00.000Z", createdAt: date, archived: false, isActive: false },
        { id: "session-git", title: "审阅最近的代码改动", updatedAt: "2026-10-08T08:00:00.000Z", createdAt: date, archived: false, isActive: false },
      ] },
      { id: "project-docs", name: "Docs", workspaceRoot: "C:\\work\\Docs", pinned: false, isActive: false, sessions: [
        { id: "session-docs", title: "补充项目开发文档", updatedAt: "2026-10-07T08:00:00.000Z", createdAt: date, archived: false, isActive: false },
      ] },
    ];
    const calls: Array<{ name: string; args: any[] }> = [];
    const listeners = new Map<string, Set<(event: any) => void>>();
    const tasks = new Map<string, any>();
    const histories = new Map<string, any[]>();
    if (initial.history.length) histories.set("project-echo:session-new", structuredClone(initial.history));
    const snapshot = () => structuredClone(state);
    const historyKey = (projectID: string, sessionID: string) => `${projectID}:${sessionID}`;
    const emit = (name: string, event: any) => listeners.get(name)?.forEach((handler) => handler(structuredClone(event)));
    const record = (name: string, ...args: any[]) => calls.push({ name, args: structuredClone(args) });
    const timelineControls = { error: "", actionError: "", delay: 0, actionDelay: 0 };
    const originalCheckpoints = [1, 2, 3].map(turn => ({ id: `checkpoint-${turn}`, turnIndex: turn, label: ["建立项目基线", "调整设置与组件", "完成界面验证"][turn - 1], preview: "界面测试检查点", timestamp: `2026-10-09T08:0${turn}:00.000Z`, isCurrent: turn === 3 }));
    const timelines = new Map<string, any>([
      ["project-echo:session-new", initial.timeline ? { checkpoints: structuredClone(originalCheckpoints), branches: [{ leafId: "checkpoint-3", label: "完成界面验证", turnCount: 3, timestamp: date, isCurrent: true }] } : { checkpoints: [], branches: [] }],
      ["project-echo:session-api", { checkpoints: [{ id: "api-checkpoint", label: "接口会话的检查点", preview: "", turnIndex: 1, timestamp: date, isCurrent: true }], branches: [] }],
    ]);
    const readTimeline = async (name: string) => {
      const key = historyKey(state.activeProjectId, state.activeSessionId);
      const result = structuredClone(timelines.get(key) || { checkpoints: [], branches: [] });
      const error = timelineControls.error;
      record(name, state.activeProjectId, state.activeSessionId);
      if (timelineControls.delay) await new Promise(resolve => setTimeout(resolve, timelineControls.delay));
      if (error) throw new Error(error);
      return result;
    };
    const billingReport = (id: string) => {
      if (!billingReports.has(id)) {
        const provider = state.runtimeSettings.model.providers.find((item: any) => item.id === id);
        billingReports.set(id, { ...structuredClone(initial.billing), providerId: id, providerName: provider?.name || id, providerKind: id === "audit-openai" ? "openai" : "custom", officialProvider: id === "audit-openai", scope: "供应商账单周期", estimatedCost: 1.2, officialUsage: [] });
      }
      return structuredClone(billingReports.get(id));
    };
    const switchSession = (projectID: string, sessionID: string) => {
      record("switch", projectID, sessionID);
      state.activeProjectId = projectID;
      state.activeSessionId = sessionID;
      for (const project of projects) {
        project.isActive = project.id === projectID;
        if (project.isActive) state.runtimeSettings.workspaceRoot = project.workspaceRoot;
        for (const session of project.sessions) session.isActive = project.isActive && session.id === sessionID;
      }
      return snapshot();
    };
    const resultFor = (task: any, content: string) => ({ content, model: "deepseek-v4-flash", usage: state.usageMetrics, state: { ...snapshot(), activeProjectId: task.projectId, activeSessionId: task.sessionId }, parts: [{ kind: "text", text: content }], turnCommitted: true, durationMs: 1800 });
    const complete = (taskID: string, content = "已完成代码审阅。当前修改没有发现阻塞问题。", reasoning?: string) => {
      const task = tasks.get(taskID);
      if (!task) throw new Error("Unknown test task");
      const key = historyKey(task.projectId, task.sessionId);
      histories.set(key, [...(histories.get(key) || []), { id: `answer-${taskID}`, role: "assistant", content, reasoning, createdAt: date }]);
      tasks.delete(taskID);
      emit("chat:task", { ...task, type: "completed", result: { ...resultFor(task, content), reasoning } });
    };
    const app = {
      GetWorkbenchState: async () => snapshot(),
      GetExtensionCatalog: async () => structuredClone(catalog),
      RefreshExtensionCatalog: async () => {
        record("extension-refresh");
        if (catalogControls.error) throw new Error(catalogControls.error);
        return structuredClone(catalog);
      },
      InstallExtension: async (id: string) => {
        record("extension-install", id);
        const item = catalog.packages.find((entry: any) => entry.id === id);
        if (!item) throw new Error("Unknown fixture extension");
        item.installed = { id, type: item.type, name: item.name, version: item.manifest.version, platform: "windows", arch: "amd64", installDir: `C:\\fixture\\extensions\\${id}`, executable: "", installedAt: date };
        item.updateAvailable = false;
        return { catalog: structuredClone(catalog), state: snapshot() };
      },
      UninstallExtension: async (id: string) => {
        record("extension-uninstall", id);
        const item = catalog.packages.find((entry: any) => entry.id === id);
        if (item) { delete item.installed; item.updateAvailable = false; }
        return { catalog: structuredClone(catalog), state: snapshot() };
      },
      RevealExtension: async (id: string) => { record("extension-reveal", id); },
      RunExtensionProjectAction: async (id: string, actionID: string) => {
        record("extension-action", id, actionID);
        return { id: actionID, output: "索引状态：就绪", exitCode: 0, durationMs: 12 };
      },
      GetProjectTree: async () => structuredClone(projects),
      ListProjects: async () => structuredClone(projects),
      ListSessions: async () => structuredClone(projects.find((project) => project.isActive)?.sessions || []),
      GetSessionMessages: async () => structuredClone(histories.get(historyKey(state.activeProjectId, state.activeSessionId)) || []),
      GetSessionMessagesForProjectSession: async (projectID: string, sessionID: string) => structuredClone(histories.get(historyKey(projectID, sessionID)) || []),
      GetActiveChatTasks: async () => [...tasks.values()].map((task) => ({ ...task, status: "running" })),
      GetActiveChatTask: async () => [...tasks.values()][0] || null,
      SwitchProjectSession: async (projectID: string, sessionID: string) => switchSession(projectID, sessionID),
      SwitchProject: async (projectID: string) => switchSession(projectID, projects.find((project) => project.id === projectID)!.sessions[0].id),
      SetReasoningLevel: async (level: string) => {
        record("reasoning", level);
        state.reasoning = state.reasoningOptions.find((option: any) => option.id === level) || state.reasoning;
        return snapshot();
      },
      SaveModelProviderAPIKey: async (id: string, key: string) => {
        record("save-provider-key", id, key);
        await new Promise(resolve => setTimeout(resolve, 80));
        const provider = state.runtimeSettings.model.providers.find((item: any) => item.id === id);
        provider.apiKeyConfigured = true;
        if (id === "deepseek") state.deepSeek.configured = true;
        return snapshot();
      },
      ClearModelProviderAPIKey: async (id: string) => {
        record("clear-provider-key", id);
        state.runtimeSettings.model.providers.find((item: any) => item.id === id).apiKeyConfigured = false;
        if (id === "deepseek") state.deepSeek.configured = false;
        return snapshot();
      },
      RefreshModelProviderModels: async (id: string) => {
        record("provider-sync", id);
        const provider = state.runtimeSettings.model.providers.find((item: any) => item.id === id);
        provider.lastSyncStatus = "ok";
        provider.lastSyncMessage = "连接测试通过，模型列表已更新。";
        if (id === "deepseek") state.deepSeek.lastCheckStatus = "ok";
        return snapshot();
      },
      SaveDeepSeekAPIKey: async (key: string) => {
        record("save-key", key);
        await new Promise((resolve) => setTimeout(resolve, 120));
        state.deepSeek.configured = true;
        state.deepSeek.lastCheckMessage = "密钥已保存。";
        return snapshot();
      },
      TestDeepSeekConnection: async () => {
        record("test-connection");
        await new Promise((resolve) => setTimeout(resolve, 120));
        state.deepSeek.lastCheckStatus = "ok";
        state.deepSeek.lastCheckMessage = "连接测试通过。";
        return snapshot();
      },
      ClearDeepSeekAPIKey: async () => {
        record("clear-key");
        state.deepSeek.configured = false;
        state.deepSeek.lastCheckStatus = "idle";
        state.deepSeek.lastCheckMessage = "保存密钥后即可测试连接。";
        return snapshot();
      },
      SaveRuntimeSettings: async (settings: any) => {
        record("settings", settings);
        if (uiControls.saveError) throw new Error(uiControls.saveError);
        state.runtimeSettings = settings;
        return snapshot();
      },
      RefreshMCPServer: async (id: string) => { record("mcp-refresh", id); return snapshot(); },
      RefreshPlugins: async () => { record("plugin-refresh"); return snapshot(); },
      RevealPlugin: async (id: string) => { record("plugin-reveal", id); },
      UninstallPlugin: async (id: string) => { record("plugin-uninstall", id); state.plugins = state.plugins.filter((plugin: any) => plugin.id !== id); return snapshot(); },
      GetUsageBillingReport: async (id: string) => {
        if (uiControls.billingError) throw new Error(uiControls.billingError);
        record("billing-read", id);
        return billingReport(id);
      },
      SyncUsageBilling: async (input: any) => {
        record("billing-sync", input);
        const report = { ...billingReport(input.providerId), ...input, officialCost: 1.25, absoluteDifference: .05, officialInputTokens: 24000, officialOutputTokens: 1200, officialRequests: 3, reconciliationSource: "界面测试账单", status: "verified", message: "账单已核对。" };
        billingReports.set(input.providerId, report);
        return structuredClone(report);
      },
      ReconcileUsageBilling: async (input: any) => {
        record("billing-reconcile", input);
        const previous = billingReport(input.providerId);
        const report = { ...previous, ...input, absoluteDifference: Math.abs(previous.estimatedCost - input.officialCost), reconciliationSource: input.source, status: "custom_reconciled", message: "已保存录入的账单金额。" };
        billingReports.set(input.providerId, report);
        return structuredClone(report);
      },
      SetPlanMode: async (enabled: boolean) => { record("plan", enabled); state.planMode = enabled; return snapshot(); },
      GetAutomationState: async () => {
        if (uiControls.automationError) throw new Error(uiControls.automationError);
        return structuredClone(automation);
      },
      SaveAutomationTask: async (task: any) => {
        record("automation-save", task);
        if (uiControls.automationSaveError) throw new Error(uiControls.automationSaveError);
        const saved = { ...task, id: task.id || `automation-${calls.length}`, createdAt: task.createdAt || date };
        automation.tasks = [...automation.tasks.filter(item => item.id !== saved.id), saved];
        return structuredClone(automation);
      },
      NewSession: async () => {
        record("new-session");
        if (uiControls.newSessionDelay) await new Promise(resolve => setTimeout(resolve, uiControls.newSessionDelay));
        const project = projects.find((project) => project.id === state.activeProjectId)!;
        const id = `session-${project.sessions.length}`;
        project.sessions.unshift({ id, title: "新对话", updatedAt: date, createdAt: date, archived: false, isActive: false });
        return switchSession(project.id, id);
      },
      DeleteProjectSession: async (projectID: string, sessionID: string) => {
        record("delete", projectID, sessionID);
        const project = projects.find((item) => item.id === projectID)!;
        project.sessions = project.sessions.filter((session) => session.id !== sessionID);
        return switchSession(projectID, project.sessions[0].id);
      },
      StartChatMessageForProjectSessionWithAttachments: async (projectID: string, sessionID: string, prompt: string, attachments: any[]) => {
        record("send", projectID, sessionID, prompt, attachments);
        const taskID = `task-${calls.length}`;
        const task = { taskId: taskID, projectId: projectID, sessionId: sessionID, startedAt: new Date().toISOString() };
        tasks.set(taskID, task);
        const key = historyKey(projectID, sessionID);
        histories.set(key, [...(histories.get(key) || []), { id: `user-${taskID}`, role: "user", content: prompt, createdAt: date, attachments }]);
        setTimeout(() => {
          if (!tasks.has(taskID)) return;
          emit("chat:task", { ...task, type: "started", message: "正在准备上下文" });
          emit("chat:task", { ...task, type: "delta", delta: "我正在检查工作区的改动。", model: "deepseek-v4-flash" });
        }, 80);
        return taskID;
      },
      StopChatMessage: async (taskID: string) => {
        record("stop", taskID);
        const task = tasks.get(taskID);
        if (!task) return false;
        tasks.delete(taskID);
        emit("chat:task", { ...task, type: "cancelled", result: resultFor(task, "已停止检查，当前进度已保留。") });
        return true;
      },
      RespondApproval: async (...args: any[]) => { record("approval", ...args); },
      ...(initial.timelineAvailable ? {
        ListCheckpoints: async () => (await readTimeline("checkpoints")).checkpoints,
        ListBranches: async () => (await readTimeline("branches")).branches,
        RewindToCheckpoint: async (id: string) => {
          record("rewind", id);
          if (timelineControls.actionDelay) await new Promise(resolve => setTimeout(resolve, timelineControls.actionDelay));
          if (timelineControls.actionError) throw new Error(timelineControls.actionError);
          const key = historyKey(state.activeProjectId, state.activeSessionId);
          const timeline = timelines.get(key);
          const target = timeline?.checkpoints.find((item: any) => item.id === id);
          if (!target) throw new Error("Unknown checkpoint");
          timeline.checkpoints = timeline.checkpoints.filter((item: any) => item.turnIndex <= target.turnIndex).map((item: any) => ({ ...item, isCurrent: item.id === id }));
          timeline.branches.forEach((branch: any) => { branch.isCurrent = branch.leafId === id; });
          state.deepSeekSession.turnCount = target.turnIndex;
          histories.set(key, [{ id: "rewound-answer", role: "assistant", content: "已恢复第 1 轮的对话。", createdAt: date }]);
          return snapshot();
        },
        SwitchBranch: async (id: string) => {
          record("branch-switch", id);
          if (timelineControls.actionError) throw new Error(timelineControls.actionError);
          const key = historyKey(state.activeProjectId, state.activeSessionId);
          const timeline = timelines.get(key);
          timeline.checkpoints = structuredClone(originalCheckpoints);
          timeline.branches.forEach((branch: any) => { branch.isCurrent = branch.leafId === id; });
          state.deepSeekSession.turnCount = 3;
          histories.set(key, [{ id: "restored-answer", role: "assistant", content: "已恢复原来的第 3 轮进度。", createdAt: date }]);
          return snapshot();
        },
      } : {}),
    };
    Object.assign(window, {
      go: { main: { App: app } },
      runtime: {
        EventsOn(name: string, handler: (event: any) => void) {
          const group = listeners.get(name) || new Set(); group.add(handler); listeners.set(name, group);
          return () => group.delete(handler);
        },
        WindowIsMaximised: async () => false, WindowMinimise() {}, WindowToggleMaximise() {}, Quit() {},
      },
      __workbenchTest: { calls, emit, complete, tasks, histories, projects, state, automation, catalog, catalogControls, uiControls, timelineControls, timelines },
    });
  }, { ...seed, history: options.history ?? [], timeline: options.timeline ?? false, timelineAvailable: options.timelineAvailable ?? true, conversationView: options.conversationView ?? "recent" });
  await page.reload();
  await page.locator('.mh-shell[aria-busy="false"]').waitFor();
}

export async function openWorkbenchPage(page: Page, name: string) {
  const aliases: Record<string, string> = { "模型设置": "模型与连接", "常规": "模型与连接", "配置": "权限与执行", "环境": "工作区与环境", "索引库": "上下文诊断", "扩展中心": "发现扩展" };
  const label = aliases[name] || name;
  const extension = ["发现扩展", "MCP 服务器", "插件", "技能与工具"].includes(label);
  const rail = page.getByRole("navigation", { name: "工作台导航" });
  if (label === "自动化任务") { await rail.getByRole("button", { name: label, exact: true }).click(); return; }
  await rail.getByRole("button", { name: extension ? "扩展与工具" : "设置", exact: true }).click();
  await page.locator(".settings-page-head h1").waitFor();
  const navigation = page.getByRole("navigation", { name: extension ? "扩展分类" : "设置分类" });
  if (!extension && !(await navigation.isVisible()) && !(await page.getByRole("combobox", { name: "设置分类", exact: true }).isVisible())) {
    await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  }
  if (await navigation.isVisible()) await navigation.getByRole("button", { name: label, exact: true }).click();
  else {
    const value = await page.getByRole("combobox", { name: "设置分类", exact: true }).locator("option").filter({ hasText: label }).getAttribute("value");
    await page.getByRole("combobox", { name: "设置分类", exact: true }).selectOption(value!);
  }
}

export async function callsFor(page: Page, name: string) {
  return page.evaluate((key) => (window as any).__workbenchTest.calls.filter((call: any) => call.name === key), name);
}
