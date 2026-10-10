import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

test("cache chip distinguishes session totals, latest request and absent data", async ({ page }) => {
  await installWorkbenchFixture(page, { audit: true });
  const chip = page.getByRole("button", { name: "缓存命中率", exact: true });
  await expect(chip).toHaveText("80%");
  await chip.click();
  const popover = page.getByRole("dialog", { name: "缓存命中率", exact: true });
  await expect(popover).toContainText("60,000 tokens");
  await expect(popover).toContainText("15,000 tokens");
  await expect(popover).toContainText("90%");
  await page.keyboard.press("Escape");
  await expect(chip).toBeFocused();
  await page.evaluate(() => {
    const test = (window as any).__workbenchTest;
    Object.assign(test.state.deepSeekSession, { sessionCacheHitTokens: 0, sessionCacheMissTokens: 0 });
    test.state.usageMetrics = { inputTokens: 50, outputTokens: 10, promptCacheHitTokens: 0, promptCacheMissTokens: 0 };
  });
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await expect(chip).toHaveText("—");
  await chip.click();
  await expect(popover).toContainText("暂无缓存数据");
  await popover.getByRole("button", { name: "查看使用统计", exact: true }).click();
  await expect(page.locator(".app-titlebar-name")).toHaveText("使用统计");
  await expect(popover).not.toBeVisible();
});

test("permission choices persist the full policy and preserve failed changes", async ({ page }) => {
  await installWorkbenchFixture(page);
  const trigger = page.getByRole("button", { name: "任务模式与权限", exact: true });
  await trigger.click();
  const request = page.getByRole("menuitemradio", { name: "请求批准", exact: true });
  await expect(request).toBeChecked();
  await page.getByRole("menuitemradio", { name: "只读", exact: true }).click();
  await expect(page.locator(".composer-permission")).toHaveText("只读");
  let saved = (await callsFor(page, "settings")).at(-1)?.args[0];
  expect(saved).toMatchObject({ sandboxMode: "read-only", filesystemAccess: "read-only", approvalPolicy: "on-request" });
  if (!(await page.getByRole("menuitemradio", { name: "完全访问", exact: true }).isVisible())) await trigger.click();
  await page.getByRole("menuitemradio", { name: "完全访问", exact: true }).click();
  await expect(page.locator(".composer-permission")).toHaveText("完全访问");
  saved = (await callsFor(page, "settings")).at(-1)?.args[0];
  expect(saved).toMatchObject({ sandboxMode: "danger-full-access", filesystemAccess: "unrestricted", networkAccess: true, shellAccess: true, approvalPolicy: "never" });
  await page.evaluate(() => { (window as any).__workbenchTest.uiControls.saveError = "权限保存失败"; });
  if (!(await request.isVisible())) await trigger.click();
  await request.click();
  await expect(page.locator(".composer-permission")).toHaveText("完全访问");
  await expect(page.getByRole("alert")).toContainText("权限保存失败");
});

test("external file approval shows the concrete target and operation before consent", async ({ page }) => {
  await installWorkbenchFixture(page);
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    fixture.emit("approval:request", { id: "external-fixture", tool: "write_file", kind: "file", path: "C:\\fixture-external\\result.txt", summary: "允许写入此工作区外文件？", command: '{"path":"C:/fixture-external/result.txt","content":"test output"}' });
  });
  const dialog = page.getByRole("dialog", { name: "文件修改请求", exact: true });
  await expect(dialog.getByLabel("请求访问的文件")).toContainText("C:\\fixture-external\\result.txt");
  await expect(dialog.getByLabel("请求执行的文件操作")).toContainText("test output");
  await dialog.getByRole("button", { name: "拒绝", exact: true }).click();
  expect((await callsFor(page, "approval")).at(-1)?.args).toEqual(["external-fixture", "write_file", false, "once"]);
});

for (const theme of ["dark", "light"] as const) {
  test(`${theme} model context inputs align without a source subtitle`, async ({ page }, testInfo) => {
    await installWorkbenchFixture(page, { audit: true });
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await openWorkbenchPage(page, "模型与连接");
    for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "添加模型", exact: true }).click();
    const rows = page.locator(".provider-model-row");
    await expect(rows.first()).toBeVisible();
    for (let i = 0; i < Math.min(3, await rows.count()); i++) {
      const row = rows.nth(i);
      const input = row.locator(".model-context-input");
      await input.fill("1000000");
      await expect(input).toHaveAttribute("title", /来源：手动设置/);
      await expect(row.getByText("手动设置", { exact: true })).toHaveCount(0);
      const numeric = (await input.boundingBox())!, model = (await row.locator(":scope > input").first().boundingBox())!;
      const bounds = (await row.boundingBox())!;
      expect(Math.abs(numeric.y - model.y)).toBeLessThan(2);
      expect(bounds.height).toBeLessThanOrEqual(numeric.height + 24);
    }
    await rows.first().scrollIntoViewIfNeeded();
    await page.screenshot({ path: testInfo.outputPath(`models-${theme}.png`) });
    await page.setViewportSize({ width: 720, height: 820 });
    const context = rows.first().locator(".model-context-input");
    expect(await context.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  });
}
