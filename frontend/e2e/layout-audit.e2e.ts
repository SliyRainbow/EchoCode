import { expect, test } from "@playwright/test";
import { installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

const splitPages = [
  ["MCP 服务器", ".mcp-server-list-section", ".mcp-server-editor-section"],
  ["插件", ".plugin-list-section", ".plugin-detail-section"],
  ["模型与连接", ".model-provider-section", ".model-editor-section"],
  ["自动化任务", ".automation-task-list-section", ".automation-task-editor-section"],
] as const;

test("workbench file, terminal, timeline and image views keep usable controls", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await installWorkbenchFixture(page, { audit: true });
  for (const width of [1440, 960, 720]) {
    await page.setViewportSize({ width, height: 900 });
    await page.getByRole("button", { name: "Git 与终端", exact: true }).click();
    const tools = page.getByRole("region", { name: "工作区工具" });
    await expect(tools).toBeVisible();
    const resizer = page.getByRole("separator", { name: "调整终端与 Git 面板高度" });
    await expect(resizer).toHaveAttribute("aria-valuenow", "280");
    await resizer.focus();
    await resizer.press("ArrowUp");
    await expect(resizer).toHaveAttribute("aria-valuenow", "296");
    await resizer.dblclick();
    await tools.getByRole("tab", { name: "终端", exact: true }).click();
    await expect(page.getByRole("textbox", { name: "终端命令" })).toBeVisible();
    const bounds = (await tools.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
    await page.getByRole("button", { name: "关闭工作区工具", exact: true }).click();
    await page.getByRole("button", { name: "文件与更改", exact: true }).click();
    await expect(page.getByRole("button", { name: "关闭文件面板" })).toBeVisible();
    await page.getByRole("button", { name: "关闭文件面板" }).click();
  }
  await page.getByRole("button", { name: "回退时间线", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "回退时间线", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "回退时间线", exact: true })).not.toBeVisible();
  await page.getByRole("button", { name: "添加附件与输入操作" }).click();
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("menuitem", { name: "添加图片", exact: true }).click();
  await (await chooser).setFiles({ name: "preview.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP3sAAAAASUVORK5CYII=", "base64") });
  await page.locator(".composer-attachments img").dblclick();
  const dialog = page.getByRole("dialog", { name: "查看图片 preview.png", exact: true });
  await expect(dialog).toBeVisible();
  for (let index = 0; index < 6; index++) await page.keyboard.press("Tab");
  expect(await dialog.evaluate(element => element.contains(document.activeElement))).toBe(true);
  const modal = (await dialog.boundingBox())!;
  expect(modal.x).toBeGreaterThanOrEqual(0);
  expect(modal.x + modal.width).toBeLessThanOrEqual(720);
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  expect(errors).toEqual([]);
});

for (const theme of ["light", "dark"] as const) {
  test(`${theme} all settings pages have space for their panes and controls`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await installWorkbenchFixture(page, { audit: true });
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const nav = page.getByRole("navigation", { name: "设置分类" });
    await expect(nav.getByRole("button")).toHaveCount(13);
    const categories = [...await nav.getByRole("button").allTextContents(), "发现扩展", "MCP 服务器", "插件", "技能与工具", "自动化任务"];
    const audit: unknown[] = [];
    for (const width of [1920, 1280, 960]) {
      await page.setViewportSize({ width, height: 1000 });
      for (const name of categories) {
        const category = name.trim();
        await openWorkbenchPage(page, category);
        await expect(page.locator(".settings-page-head h1")).toHaveText(category);
        const body = page.locator(".settings-content > .settings-page-body");
        await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
        const dimensions = await body.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth, height: element.clientHeight }));
        expect.soft(dimensions.scroll, `${category} at ${width}px overflows horizontally`).toBeLessThanOrEqual(dimensions.client + 1);
        const footer = page.locator(".settings-save-footer");
        if (await footer.count()) {
          const bodyBox = (await body.boundingBox())!;
          const footerBox = (await footer.boundingBox())!;
          expect.soft(bodyBox.y + bodyBox.height, `${category} save bar covers the form`).toBeLessThanOrEqual(footerBox.y + 1);
          await expect.soft(page.getByRole("button", { name: "保存设置", exact: true })).toHaveCount(1);
        }
        const split = splitPages.find(([label]) => label === category);
        if (split) {
          const sidebar = (await page.locator(split[1]).boundingBox())!;
          const detail = (await page.locator(split[2]).boundingBox())!;
          const sideBySide = detail.x > sidebar.x + 2;
          const gap = sideBySide ? detail.x - sidebar.x - sidebar.width : detail.y - sidebar.y - sidebar.height;
          expect.soft(gap, `${category} at ${width}px has no separation between panes`).toBeGreaterThanOrEqual(23);
          audit.push({ category, width, gap, sideBySide, ...dimensions });
        } else audit.push({ category, width, ...dimensions });
        if (width === 1920 && ["MCP 服务器", "插件", "使用统计", "AI 团队", "自动化任务", "技能与工具", "浏览器", "电脑操控"].includes(category)) {
          await page.mouse.move(950, 38);
          await page.screenshot({ path: testInfo.outputPath(`${theme}-${category}.png`) });
        }
      }
    }
    await testInfo.attach("settings-layout-audit.json", { body: JSON.stringify(audit, null, 2), contentType: "application/json" });
    await page.setViewportSize({ width: 720, height: 800 });
    for (const category of ["外观", "权限与执行", "模型与连接", "MCP 服务器", "插件", "AI 团队", "技能与工具", "自动化任务", "使用统计"]) {
      await openWorkbenchPage(page, category);
      const body = page.locator(".settings-content > .settings-page-body");
      const size = await body.evaluate(element => ({ width: element.clientWidth, scroll: element.scrollWidth }));
      expect.soft(size.scroll, `${category} overflows at 720px`).toBeLessThanOrEqual(size.width + 1);
    }
    await page.evaluate(() => localStorage.setItem("echocode:ui-appearance-v1", JSON.stringify({ scaleMode: "manual", manualScale: 125 })));
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.reload();
    await page.getByRole("button", { name: "设置", exact: true }).click();
    for (const [category, sidebarSelector, detailSelector] of splitPages) {
      await openWorkbenchPage(page, category);
      const sidebar = (await page.locator(sidebarSelector).boundingBox())!;
      const detail = (await page.locator(detailSelector).boundingBox())!;
      expect.soft(detail.y - sidebar.y - sidebar.height, `${category} should stack with a gap at 125% scale`).toBeGreaterThanOrEqual(28);
    }
    expect(errors).toEqual([]);
  });
}
