import { expect, test, type Page } from "@playwright/test";
import { callsFor, installWorkbenchFixture } from "./workbench-fixture";

const timeline = (page: Page) => page.getByRole("dialog", { name: "回退时间线", exact: true });

for (const theme of ["light", "dark"] as const) {
  test(`${theme} notices and timeline headers remain readable at different widths and scales`, async ({ page }, testInfo) => {
    await installWorkbenchFixture(page, { timeline: true });
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await page.getByRole("button", { name: "内置浏览器", exact: true }).click();
    const notice = page.locator(".workbench-notice");
    await expect(notice).toHaveAttribute("role", "status");
    await expect(notice).toHaveAttribute("data-tone", "info");
    await expect(notice).toHaveCSS("font-weight", "400");
    await expect(notice).toHaveCSS("font-size", "12px");
    await page.screenshot({ path: testInfo.outputPath(`${theme}-notice.png`) });
    await page.getByRole("button", { name: "关闭错误提示", exact: true }).click();
    await expect(notice).toHaveCount(0);
    await page.evaluate(() => { (window as any).go.main.App.OpenBrowserURL = async () => { throw new Error("连接失败（测试）"); }; });
    await page.getByRole("button", { name: "内置浏览器", exact: true }).click();
    await expect(notice).toHaveAttribute("role", "alert");
    await expect(notice).toHaveAttribute("data-tone", "error");
    await expect(notice).toHaveCSS("font-weight", "400");
    await page.getByRole("button", { name: "关闭错误提示", exact: true }).click();
    for (const width of [1440, 960, 720]) {
      await page.setViewportSize({ width, height: 820 });
      const trigger = page.getByRole("button", { name: "回退时间线", exact: true });
      await trigger.click();
      const panel = timeline(page);
      await expect(panel.getByRole("heading", { name: "回退时间线", exact: true })).toBeVisible();
      await expect(panel.locator(".timeline-item")).toHaveCount(3);
      const titlebar = (await page.locator(".app-titlebar").boundingBox())!;
      const bounds = (await panel.boundingBox())!;
      const close = (await panel.getByRole("button", { name: "关闭回退时间线", exact: true }).boundingBox())!;
      expect(bounds.y).toBeGreaterThanOrEqual(titlebar.y + titlebar.height - 1);
      expect(close.y).toBeGreaterThanOrEqual(bounds.y);
      expect(bounds.x).toBeGreaterThanOrEqual(0);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width + 1);
      expect(bounds.y + bounds.height).toBeLessThanOrEqual(821);
      if (width === 1440) {
        await panel.getByRole("button", { name: "关闭回退时间线", exact: true }).evaluate(element => (element as HTMLElement).blur());
        await page.screenshot({ path: testInfo.outputPath(`${theme}-timeline.png`) });
      }
      await page.keyboard.press("Escape");
      await expect(panel).not.toBeVisible();
      await expect(trigger).toBeFocused();
    }
    await page.evaluate(() => localStorage.setItem("echocode:ui-appearance-v1", JSON.stringify({ scaleMode: "manual", manualScale: 125 })));
    await page.reload();
    await page.getByRole("button", { name: "回退时间线", exact: true }).click();
    const panel = timeline(page);
    await expect(panel.getByRole("heading", { name: "回退时间线", exact: true })).toBeVisible();
    const bar = (await page.locator(".app-titlebar").boundingBox())!;
    const bounds = (await panel.boundingBox())!;
    expect(bounds.y).toBeGreaterThanOrEqual(bar.y + bar.height - 1);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(721);
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(821);
  });
}

test("web preview explains timeline availability instead of promising checkpoints", async ({ page }) => {
  await installWorkbenchFixture(page, { timelineAvailable: false });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  await expect(timeline(page)).toContainText("请在桌面应用中查看时间线");
  await expect(timeline(page)).not.toContainText("完成一轮对话后");
  await expect(timeline(page).getByRole("button", { name: "刷新时间线", exact: true })).toBeDisabled();
});

test("rewind confirms its scope and the retained tip can be restored", async ({ page }) => {
  await installWorkbenchFixture(page, { timeline: true });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  const panel = timeline(page);
  await panel.getByRole("button", { name: "回退到第 1 轮", exact: true }).click();
  const confirm = page.getByRole("dialog", { name: "回退到第 1 轮？", exact: true });
  await expect(confirm.getByRole("button", { name: "取消", exact: true })).toBeFocused();
  await expect(confirm).toContainText("建立项目基线");
  await page.keyboard.press("Escape");
  await expect(confirm).not.toBeVisible();
  await expect(panel).toBeVisible();
  expect(await callsFor(page, "rewind")).toHaveLength(0);
  await panel.getByRole("button", { name: "回退到第 1 轮", exact: true }).click();
  await confirm.getByRole("button", { name: "确认回退", exact: true }).click();
  await expect(panel).toContainText("已回退到第 1 轮。");
  expect((await callsFor(page, "rewind"))[0].args).toEqual(["checkpoint-1"]);
  await expect(panel.locator(".timeline-item.current")).toContainText("建立项目基线");
  const branch = panel.getByRole("button", { name: "切换到分支 完成界面验证", exact: true });
  await expect(branch).toBeEnabled();
  await branch.click();
  await page.getByRole("dialog", { name: "切换对话分支？", exact: true }).getByRole("button", { name: "切换分支", exact: true }).click();
  await expect(panel).toContainText("已恢复所选对话分支的进度。");
  expect((await callsFor(page, "branch-switch"))[0].args).toEqual(["checkpoint-3"]);
  await expect(panel.locator(".timeline-item.current")).toContainText("完成界面验证");
  await expect(page.locator(".op-msg.assistant")).toContainText("已恢复原来的第 3 轮进度。");
});

test("loading and action failures stay visible and can be retried", async ({ page }) => {
  await installWorkbenchFixture(page, { timeline: true });
  await page.evaluate(() => { Object.assign((window as any).__workbenchTest.timelineControls, { error: "记录暂时不可读", delay: 300 }); });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  const panel = timeline(page);
  await expect(panel.getByRole("status")).toContainText("正在读取时间线");
  await expect(panel.getByRole("alert")).toContainText("记录暂时不可读");
  await expect(panel).not.toContainText("此对话还没有检查点");
  await page.evaluate(() => { Object.assign((window as any).__workbenchTest.timelineControls, { error: "", delay: 0 }); });
  await panel.getByRole("button", { name: "重新加载时间线", exact: true }).click();
  await expect(panel.locator(".timeline-item")).toHaveCount(3);
  await page.evaluate(() => { (window as any).__workbenchTest.timelineControls.actionError = "文件已在其他程序中修改（测试）"; });
  await panel.getByRole("button", { name: "回退到第 1 轮", exact: true }).click();
  await page.getByRole("dialog", { name: "回退到第 1 轮？", exact: true }).getByRole("button", { name: "确认回退", exact: true }).click();
  await expect(panel.getByRole("alert")).toContainText("文件已在其他程序中修改");
  await expect(panel).not.toContainText("已回退到第 1 轮。");
  await expect(panel.getByRole("button", { name: "关闭回退时间线", exact: true })).toBeEnabled();
});

test("a delayed result from another session cannot replace the current timeline", async ({ page }) => {
  await installWorkbenchFixture(page, { timeline: true });
  await page.evaluate(() => { (window as any).__workbenchTest.timelineControls.delay = 700; });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  await expect(timeline(page).getByRole("status")).toContainText("正在读取时间线");
  await page.keyboard.press("Control+k");
  await page.getByRole("combobox", { name: "搜索对话与项目" }).fill("API");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(timeline(page)).not.toBeVisible();
  await page.evaluate(() => { (window as any).__workbenchTest.timelineControls.delay = 0; });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  await expect(timeline(page)).toContainText("接口会话的检查点");
  await page.waitForTimeout(750);
  await expect(timeline(page)).not.toContainText("完成界面验证");
  await expect(timeline(page).locator(".timeline-item")).toHaveCount(1);
});

test("long timelines scroll below their header and running tasks cannot be rewound", async ({ page }) => {
  await installWorkbenchFixture(page, { timeline: true });
  await page.evaluate(() => {
    const data = (window as any).__workbenchTest.timelines.get("project-echo:session-new");
    data.checkpoints = Array.from({ length: 30 }, (_, index) => ({ ...data.checkpoints[0], id: `long-${index}`, turnIndex: index + 1, label: "较长的检查点摘要，用于验证列表滚动和换行布局。".repeat(2), isCurrent: index === 29 }));
  });
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  const panel = timeline(page);
  await expect(panel.locator(".timeline-item")).toHaveCount(30);
  const header = (await panel.locator(".timeline-head").boundingBox())!;
  await panel.locator(".timeline-scroll").evaluate(element => { element.scrollTop = element.scrollHeight; });
  expect((await panel.locator(".timeline-head").boundingBox())!.y).toBe(header.y);
  await expect(panel.getByRole("button", { name: "关闭回退时间线", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await editor.fill("开始检查");
  await editor.press("Enter");
  await expect(page.getByRole("button", { name: "停止生成", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  await expect(panel).toContainText("当前会话正在执行任务");
  await expect(panel.getByRole("button", { name: "回退到第 1 轮", exact: true })).toBeDisabled();
});
