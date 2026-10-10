import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

const history = [
  { id: "layout-user", role: "user", content: "检查工作区布局", createdAt: "2026-10-10T08:00:00Z" },
  { id: "layout-answer", role: "assistant", content: "## 检查结果\n\n" + "对话内容和输入框应随可用空间调整宽度，并保持一致的边距。".repeat(6) + "\n\n- 核对窗口大小\n- 核对侧栏宽度\n- 核对回复操作间距", createdAt: "2026-10-10T08:00:41Z", durationMs: 41000 },
];

for (const theme of ["dark", "light"] as const) {
  test(`${theme} conversation expands with its pane and footer actions remain separated`, async ({ page }, testInfo) => {
    await installWorkbenchFixture(page, { history });
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    const message = page.locator(".op-msg.assistant");
    const composer = page.locator(".composer-box");
    const widths: number[] = [];
    for (const width of [1100, 1440, 1920]) {
      await page.setViewportSize({ width, height: 1000 });
      const pane = (await page.locator(".chat-pane").boundingBox())!, answer = (await message.boundingBox())!, input = (await composer.boundingBox())!;
      expect(input.width).toBeGreaterThan(pane.width * .84);
      expect(answer.width).toBeGreaterThan(pane.width * .84);
      expect(Math.abs(answer.x - input.x)).toBeLessThan(2);
      expect(input.x + input.width).toBeLessThanOrEqual(pane.x + pane.width);
      widths.push(input.width);
    }
    expect(widths[1] - widths[0]).toBeGreaterThan(200);
    expect(widths[2] - widths[1]).toBeGreaterThan(300);
    const copy = message.getByRole("button", { name: "复制回复", exact: true });
    await copy.hover();
    const runtime = (await message.locator(".op-message-runtime").boundingBox())!, copyBox = (await copy.boundingBox())!;
    expect(copyBox.x - runtime.x - runtime.width).toBeGreaterThanOrEqual(12);
    expect(Math.abs(copyBox.y + copyBox.height / 2 - runtime.y - runtime.height / 2)).toBeLessThan(2);
    await page.screenshot({ path: testInfo.outputPath(`conversation-wide-${theme}.png`) });
    await page.getByRole("button", { name: "选择模型", exact: true }).click();
    const models = page.getByRole("menu", { name: "选择模型", exact: true });
    let menuBox = (await models.boundingBox())!;
    expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(1920);
    await models.getByRole("menuitemradio").last().click();
    await expect(models).not.toBeVisible();
    expect((await callsFor(page, "settings")).length).toBeGreaterThan(0);
    await page.getByRole("button", { name: "文件与更改", exact: true }).click();
    const split = (await page.locator(".chat-pane").boundingBox())!, input = (await composer.boundingBox())!;
    expect(input.width).toBeLessThan(widths[2]);
    expect(input.width).toBeGreaterThan(split.width * .84);
    expect(await page.locator(".chat-pane").evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await page.getByRole("button", { name: "关闭文件面板", exact: true }).click();
    await page.setViewportSize({ width: 720, height: 820 });
    await page.getByRole("button", { name: "选择模型", exact: true }).click();
    menuBox = (await models.boundingBox())!;
    expect(menuBox.x).toBeGreaterThanOrEqual(0);
    expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(720);
    await models.getByRole("menuitemradio").first().click();
    await expect(models).not.toBeVisible();
  });
}

test("settings divider accounts for UI scale and is hidden with the sidebar", async ({ page }) => {
  await installWorkbenchFixture(page);
  await page.evaluate(() => localStorage.setItem("echocode:ui-appearance-v1", JSON.stringify({ scaleMode: "manual", manualScale: 125 })));
  await page.reload();
  await openWorkbenchPage(page, "外观");
  const handle = page.getByRole("separator", { name: "调整侧栏宽度", exact: true });
  const nav = page.getByRole("navigation", { name: "设置分类", exact: true });
  const box = (await handle.boundingBox())!, sidebar = (await nav.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + 180);
  await page.mouse.down();
  await page.mouse.move(sidebar.x + 320 * 1.25, box.y + 180, { steps: 8 });
  await page.mouse.up();
  await expect(handle).toHaveAttribute("aria-valuenow", "320");
  expect((await nav.boundingBox())!.width).toBe(400);
  await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
  await expect(handle).not.toBeVisible();
  const panel = (await page.locator(".settings-screen").boundingBox())!, content = (await page.locator(".settings-content").boundingBox())!;
  expect(Math.abs(panel.x - content.x)).toBeLessThan(2);
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await expect(handle).toHaveAttribute("aria-valuenow", "320");
  await handle.focus(); await handle.press("Enter");
  await expect(handle).toHaveAttribute("aria-valuenow", "252");
});
