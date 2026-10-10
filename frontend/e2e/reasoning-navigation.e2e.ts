import { expect, test, type Page } from "@playwright/test";
import { installWorkbenchFixture } from "./workbench-fixture";

const trace = "正在核对设置页面的布局。\n\n检查分栏间距和不同窗口尺寸，接着核对按钮与键盘操作。";
const savedHistory = [
  { id: "saved-user", role: "user", content: "检查界面布局和交互", createdAt: "2026-10-09T08:00:00Z" },
  { id: "saved-answer", role: "assistant", content: "已完成检查。分栏间距与键盘操作均正常。", reasoning: trace, createdAt: "2026-10-09T08:00:04Z", durationMs: 4000 },
];

async function startReasoning(page: Page) {
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await editor.fill("检查界面布局和交互");
  await editor.press("Enter");
  await expect(page.locator(".op-msg.assistant")).toContainText("我正在检查工作区的改动。");
  await page.evaluate((reasoning) => {
    const bridge = (window as any).__workbenchTest;
    bridge.emit("chat:task", { ...[...bridge.tasks.values()][0], type: "reasoning", delta: reasoning });
  }, trace);
  await expect(page.getByRole("region", { name: "模型思考内容" })).toHaveText(trace);
}

test("reasoning stays readable through streaming, completion and session switches", async ({ page }) => {
  await installWorkbenchFixture(page);
  await startReasoning(page);
  const trigger = page.getByRole("button", { name: "思考过程", exact: true });
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await trigger.evaluate(element => { (window as any).__reasoningTrigger = element; });
  await page.evaluate(() => {
    const bridge = (window as any).__workbenchTest;
    bridge.emit("chat:task", { ...[...bridge.tasks.values()][0], type: "delta", delta: "开始整理检查结果。" });
  });
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await trigger.click();
  await page.evaluate(() => {
    const bridge = (window as any).__workbenchTest;
    bridge.emit("chat:task", { ...[...bridge.tasks.values()][0], type: "reasoning", delta: "\n补充检查浅色主题。" });
  });
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
  await trigger.click();
  // Older hosts may omit reasoning from the final result/history snapshot.
  await page.evaluate(() => {
    const bridge = (window as any).__workbenchTest;
    bridge.complete([...bridge.tasks.keys()][0], "布局检查完成。");
  });
  await expect(page.getByRole("button", { name: "停止生成", exact: true })).not.toBeVisible();
  await expect(page.locator(".op-reasoning")).toHaveCount(1);
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  expect(await trigger.evaluate(element => element === (window as any).__reasoningTrigger)).toBe(true);
  await expect(page.getByRole("region", { name: "模型思考内容" })).toContainText("补充检查浅色主题");
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await expect(trigger).toHaveCount(0);
  await page.locator(".recent-session").filter({ hasText: "新对话" }).click();
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("region", { name: "模型思考内容" })).toContainText(trace);
});

test("background completion preserves its trace in the originating session", async ({ page }) => {
  await installWorkbenchFixture(page);
  await startReasoning(page);
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await page.evaluate((reasoning) => {
    const bridge = (window as any).__workbenchTest;
    bridge.complete([...bridge.tasks.keys()][0], "后台检查完成。", reasoning);
  }, trace);
  await expect(page.locator(".op-reasoning")).toHaveCount(0);
  await page.locator(".recent-session").filter({ hasText: "新对话" }).click();
  await expect(page.getByRole("region", { name: "模型思考内容" })).toHaveText(trace);
  await expect(page.locator(".op-msg.assistant")).toContainText("后台检查完成。");
});

test("internal phase timings stay out of live and restored chat while reasoning and edits remain", async ({ page }, testInfo) => {
  const internalMessages = ["正在确认本轮权限与任务范围", "正在选择模型路由", "正在组装项目上下文", "正在检查上下文压缩", "正在准备模型连接", "正在等待模型首个响应", "正在执行 Agent 任务", "模型连接已建立", "已收到模型首个响应事件", "已收到模型首个文字输出"];
  await installWorkbenchFixture(page, { history: [
    savedHistory[0],
    { ...savedHistory[1], content: "已调整配置文件。", parts: [
      ...internalMessages.map((message, index) => ({ kind: "timeline_note" as const, message, toolCallId: `timing:stage-${index}`, status: "completed", durationMs: 20 })),
      { kind: "diff", path: "src/config.ts", patch: "-const enabled = false;\n+const enabled = true;", additions: 1, deletions: 1 },
      { kind: "text", text: "已调整配置文件。" },
    ] },
  ] });
  for (const message of internalMessages) await expect(page.getByText(message, { exact: true })).toHaveCount(0);
  await expect(page.locator(".op-timeline-note")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "已编辑 1 个文件" })).toBeVisible();
  await page.getByRole("button", { name: "思考过程", exact: true }).click();
  await expect(page.getByRole("region", { name: "模型思考内容" })).toContainText(trace);
  await page.screenshot({ path: testInfo.outputPath("clean-chat-reasoning-and-edits.png") });
  await page.getByRole("button", { name: "新对话", exact: true }).click();
  await startReasoning(page);
  await page.evaluate((messages) => {
    const bridge = (window as any).__workbenchTest;
    const task = [...bridge.tasks.values()][0] as any;
    messages.forEach((message, index) => bridge.emit("chat:task", { ...task, type: "status", message, toolCallId: `timing:stage-${index}`, status: "completed" }));
  }, internalMessages);
  await expect(page.locator(".op-timeline-note")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "模型思考内容" })).toContainText(trace);
});

for (const theme of ["dark", "light"] as const) {
  test(`${theme} persisted reasoning survives reload and matches the workbench`, async ({ page }, testInfo) => {
    await installWorkbenchFixture(page, { history: savedHistory });
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    const trigger = page.getByRole("button", { name: "思考过程", exact: true });
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("region", { name: "模型思考内容" })).toHaveText(trace);
    await trigger.evaluate(element => (element as HTMLElement).blur());
    await page.mouse.move(850, 35);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-reasoning.png`) });
    await page.reload();
    await expect(trigger).toBeVisible();
    await trigger.click();
    await expect(page.getByRole("region", { name: "模型思考内容" })).toHaveText(trace);
    await page.setViewportSize({ width: 720, height: 800 });
    const size = await page.locator(".op-reasoning").evaluate(element => ({ client: element.clientWidth, scroll: element.scrollWidth }));
    expect(size.scroll).toBeLessThanOrEqual(size.client + 1);
  });
}

test("navigation keeps a single entry per action and coherent back-forward history", async ({ page }) => {
  const settingsRequests: string[] = [];
  page.on("request", request => { if (request.url().includes("/src/settings-panels.tsx")) settingsRequests.push(request.url()); });
  await installWorkbenchFixture(page);
  expect(settingsRequests).toEqual([]);
  await expect(page.getByRole("button", { name: "搜索对话", exact: true })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "收起侧栏", exact: true })).toHaveCount(1);
  const rail = page.getByRole("navigation", { name: "工作台导航" });
  await expect(rail.getByRole("button", { name: "使用统计", exact: true })).toHaveCount(0);
  await rail.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.locator(".settings-page-head h1")).toHaveText("外观");
  expect(settingsRequests).toHaveLength(1);
  const categories = page.getByRole("navigation", { name: "设置分类" });
  await categories.getByRole("button", { name: "使用统计", exact: true }).click();
  await page.getByRole("button", { name: "后退", exact: true }).click();
  await expect(page.locator(".settings-page-head h1")).toHaveText("外观");
  await page.getByRole("button", { name: "前进", exact: true }).click();
  await expect(page.locator(".settings-page-head h1")).toHaveText("使用统计");
  await rail.getByRole("button", { name: "工作区", exact: true }).click();
  await rail.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.locator(".settings-page-head h1")).toHaveText("使用统计");
  await page.getByRole("button", { name: "后退", exact: true }).click();
  await rail.getByRole("button", { name: "扩展与工具", exact: true }).click();
  await expect(page.locator(".settings-page-head h1")).toHaveText("发现扩展");
  await expect(page.getByRole("button", { name: "前进", exact: true })).toBeDisabled();
});

test("surface and disclosure motion respects reduced-motion preferences", async ({ page }) => {
  await installWorkbenchFixture(page, { motion: true, history: savedHistory });
  const trigger = page.getByRole("button", { name: "思考过程", exact: true });
  await trigger.click();
  const content = page.locator(".op-reasoning-content");
  await expect(content).toHaveCSS("animation-name", "ec-disclosure-open");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.locator(".settings-screen")).toHaveCSS("animation-name", "ec-fade-in");
  await expect(page.locator(".settings-page-body")).toHaveCSS("animation-name", "ec-content-in");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(page.locator(".settings-screen")).toHaveCSS("animation-name", "none");
  await expect(page.locator(".mh-shell")).toHaveCSS("transition-duration", "0s");
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  await expect(content).toHaveCSS("animation-name", "none");
  await trigger.click();
  await expect(page.getByRole("region", { name: "模型思考内容" })).not.toBeVisible();
});
