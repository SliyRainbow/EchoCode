import { expect, test, type Page } from "@playwright/test";
import { installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

async function installContext(page: Page) {
  await installWorkbenchFixture(page);
  await page.evaluate(() => {
    const test = (window as any).__workbenchTest;
    const app = (window as any).go.main.App;
    Object.assign(test.state.deepSeekSession, { contextWindowTokens: 64000, contextWindowSource: "manual", inputBudgetTokens: 48000, estimatedInputTokens: 24000, contextUsagePercent: 50, compressionTriggerTokens: 40000, messageCount: 30, compressionCount: 0 });
    test.contextError = "";
    test.contextDelay = 0;
    app.CompressSessionContext = async (projectId: string, sessionId: string) => {
      test.calls.push({ name: "compress", args: [projectId, sessionId] });
      if (test.contextDelay) await new Promise(resolve => setTimeout(resolve, test.contextDelay));
      if (test.contextError) throw new Error(test.contextError);
      const state = structuredClone(test.state);
      state.activeProjectId = projectId; state.activeSessionId = sessionId;
      Object.assign(state.deepSeekSession, { estimatedInputTokens: 6000, contextUsagePercent: 12.5, compressionCount: 1 });
      return { projectId, sessionId, state, compressed: true, beforeTokens: 24000, afterTokens: 6000, removedMessages: 20 };
    };
  });
  // A native state refresh follows session navigation.
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await page.locator(".recent-session").filter({ hasText: "新对话" }).click();
}

test("context usage exposes estimated budget, compression, errors and statistics", async ({ page }) => {
  await installContext(page);
  const trigger = page.getByRole("button", { name: "上下文用量", exact: true });
  await expect(trigger).toHaveText("50%");
  await trigger.click();
  const popover = page.getByRole("dialog", { name: "上下文用量", exact: true });
  await expect(popover).toContainText("24,000");
  await expect(popover).toContainText("64,000");
  await expect(popover).toContainText("40,000");
  await expect(popover).toContainText("手动设置");
  await page.evaluate(() => { (window as any).__workbenchTest.contextError = "测试：存储不可写"; });
  await popover.getByRole("button", { name: "压缩上下文", exact: true }).click();
  await expect(popover.getByRole("alert")).toHaveText("测试：存储不可写");
  await expect(trigger).toHaveText("50%");
  await page.evaluate(() => { (window as any).__workbenchTest.contextError = ""; });
  await popover.getByRole("button", { name: "压缩上下文", exact: true }).click();
  await expect(popover.getByRole("status")).toContainText("24,000 → 6,000");
  await expect(trigger).toHaveText("13%");
  await popover.getByRole("button", { name: "使用统计", exact: true }).click();
  await expect(page.locator(".app-titlebar-name")).toHaveText("使用统计");
});

test("a late compression response cannot replace a different conversation", async ({ page }) => {
  await installContext(page);
  await page.evaluate(() => { (window as any).__workbenchTest.contextDelay = 600; });
  await page.getByRole("button", { name: "上下文用量", exact: true }).click();
  await page.getByRole("button", { name: "压缩上下文", exact: true }).click();
  await page.keyboard.press("Escape");
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await page.waitForTimeout(800);
  await expect(page.locator(".chat-breadcrumb strong")).toHaveText("检查 API 错误处理");
  await expect(page.getByRole("button", { name: "上下文用量", exact: true })).toHaveText("50%");
});

test("titlebar follows pages and settings reclaim the duplicate header space", async ({ page }) => {
  await installWorkbenchFixture(page);
  for (const title of ["记忆", "MCP 服务器", "插件", "自动化任务"]) {
    await openWorkbenchPage(page, title);
    await expect(page.locator(".app-titlebar-name")).toHaveText(title);
    await expect(page.locator(".settings-screen > .drawer-head")).toHaveCount(0);
    const screen = (await page.locator(".settings-screen").boundingBox())!;
    const center = (await page.locator(".settings-center").boundingBox())!;
    expect(Math.abs(center.y - screen.y)).toBeLessThan(2);
  }
  await page.getByRole("button", { name: "后退", exact: true }).click();
  await expect(page.locator(".app-titlebar-name")).toHaveText("插件");
  await page.getByRole("button", { name: "前进", exact: true }).click();
  await expect(page.locator(".app-titlebar-name")).toHaveText("自动化任务");
});

for (const theme of ["dark", "light"]) {
  test(`${theme} context controls fit narrow windows and block compression while running`, async ({ page }, testInfo) => {
    await installContext(page);
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await page.setViewportSize({ width: 700, height: 780 });
    const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
    await editor.fill("检查上下文用量"); await editor.press("Enter");
    await expect(page.getByRole("button", { name: "停止生成", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "上下文用量", exact: true }).click();
    const popover = page.getByRole("dialog", { name: "上下文用量", exact: true });
    await expect(popover.getByRole("button", { name: "压缩上下文", exact: true })).toBeDisabled();
    const bounds = (await popover.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(700);
    await expect(popover).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
    await page.screenshot({ path: testInfo.outputPath(`context-${theme}.png`) });
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "上下文用量", exact: true })).toBeFocused();
  });
}
