import { openWorkbenchPage } from "./workbench-fixture";
import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture } from "./workbench-fixture";

const errors = new WeakMap<object, string[]>();
test.beforeEach(async ({ page }) => {
  errors.set(page, []);
  page.on("pageerror", (error) => errors.get(page)!.push(error.message));
  await installWorkbenchFixture(page);
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await openWorkbenchPage(page, "外观");
});
test.afterEach(async ({ page }) => { expect(errors.get(page)).toEqual([]); });

test("connection actions save, test and remove a key with clear disabled states", async ({ page }) => {
  await openWorkbenchPage(page, "模型与连接");
  await page.getByRole("button", { name: "清除密钥", exact: true }).click();
  const secret = page.getByLabel("模型密钥", { exact: true });
  await expect(page.getByRole("button", { name: "保存密钥", exact: true })).toBeDisabled();
  await expect(secret).toBeEnabled();
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  const connect = page.getByRole("button", { name: "连接模型", exact: true });
  const workspace = page.getByRole("button", { name: "工作区设置", exact: true });
  const connectionBox = (await connect.boundingBox())!;
  const workspaceBox = (await workspace.boundingBox())!;
  expect(connectionBox.width).toBeCloseTo(workspaceBox.width, 1);
  expect(connectionBox.y + connectionBox.height).toBeLessThanOrEqual(workspaceBox.y);
  expect((await connect.locator("span").nth(1).boundingBox())!.height).toBeLessThan(25);
  await connect.click();
  await expect(secret).toHaveAttribute("type", "password");
  await secret.fill("test-key-for-ui-fixture");
  await page.getByRole("button", { name: "显示模型密钥" }).click();
  await expect(secret).toHaveAttribute("type", "text");
  await expect(secret).toHaveValue("test-key-for-ui-fixture");
  await page.getByRole("button", { name: "隐藏模型密钥" }).click();
  await expect(secret).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "保存密钥", exact: true }).click();
  await expect(secret).toHaveValue("");
  expect((await callsFor(page, "save-provider-key"))[0].args).toEqual(["deepseek", "test-key-for-ui-fixture"]);
  await page.getByRole("button", { name: "测试并获取", exact: true }).click();
  await expect(page.locator(".provider-editor")).toContainText("连接测试通过");
  expect(await callsFor(page, "provider-sync")).toHaveLength(1);
});

test("sliders support keyboard steps, limits and dragging, and retain their values", async ({ page }) => {
  const font = page.getByRole("slider", { name: "界面字号", exact: true });
  await font.focus();
  await font.press("ArrowRight");
  await expect(font).toHaveAttribute("aria-valuenow", "15");
  await font.press("End");
  await expect(font).toHaveAttribute("aria-valuenow", "18");
  await font.press("Home");
  await expect(font).toHaveAttribute("aria-valuenow", "12");
  const track = font.locator("..");
  const box = (await track.boundingBox())!;
  const thumb = (await font.boundingBox())!;
  expect(Math.abs(thumb.y + thumb.height / 2 - box.y - box.height / 2)).toBeLessThan(2);
  await page.mouse.move(thumb.x + thumb.width / 2, thumb.y + thumb.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 5 });
  await page.mouse.up();
  await expect(font).toHaveAttribute("aria-valuenow", "15");
  const lineHeight = page.getByRole("slider", { name: "正文行高", exact: true });
  await lineHeight.focus();
  await lineHeight.press("ArrowLeft");
  await expect(lineHeight).toHaveAttribute("aria-valuenow", "1.5");
  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("echocode:ui-appearance-v1")!));
  expect(stored.fontSize).toBe(15);
  expect(stored.lineHeight).toBe(1.5);
  await page.reload();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await openWorkbenchPage(page, "外观");
  await expect(font).toHaveAttribute("aria-valuenow", "15");
});

test("selects work with keyboard and enable manual scaling without clipping", async ({ page }) => {
  const trigger = page.getByRole("button", { name: /^界面缩放方式/ });
  const scale = page.getByRole("slider", { name: "手动界面缩放", exact: true });
  await expect(scale).toHaveAttribute("aria-disabled", "true");
  await trigger.focus();
  await trigger.press("Enter");
  await expect(page.getByRole("listbox")).toBeVisible();
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("listbox")).not.toBeVisible();
  await expect(trigger).toContainText("手动");
  await expect(trigger).toBeFocused();
  await scale.focus();
  await scale.press("ArrowRight");
  await expect(scale).toHaveAttribute("aria-valuenow", "105");
  await expect(page.locator("html")).toHaveAttribute("data-ui-scale-mode", "manual");
  await trigger.click();
  const popup = (await page.getByRole("listbox").boundingBox())!;
  expect(popup.x + popup.width).toBeLessThanOrEqual(1440);
  await page.getByRole("option", { name: "自动", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-ui-scale-mode", "auto");
});

test("settings sidebar divider resizes both pages, persists and resets", async ({ page }) => {
  const handle = page.getByRole("separator", { name: "调整侧栏宽度", exact: true });
  const nav = page.getByRole("navigation", { name: "设置分类" });
  await expect(page.getByRole("slider", { name: "侧栏宽度", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "恢复默认宽度", exact: true })).toHaveCount(0);
  await expect(handle).toHaveAttribute("aria-valuenow", "252");
  await handle.focus();
  await handle.press("End");
  await expect(handle).toHaveAttribute("aria-valuenow", "420");
  expect((await nav.boundingBox())!.width).toBe(420);
  await handle.press("Home");
  await expect(handle).toHaveAttribute("aria-valuenow", "220");
  expect((await nav.boundingBox())!.width).toBe(220);
  await handle.press("ArrowRight");
  await expect(handle).toHaveAttribute("aria-valuenow", "236");
  const divider = (await handle.boundingBox())!, navigation = (await nav.boundingBox())!;
  await page.mouse.move(divider.x + divider.width / 2, divider.y + 160);
  await page.mouse.down();
  await page.mouse.move(navigation.x + 352, divider.y + 160, { steps: 6 });
  await page.mouse.up();
  const width = Number(await handle.getAttribute("aria-valuenow"));
  expect(width).toBeGreaterThan(330);
  expect(width).toBeLessThan(370);
  expect((await nav.boundingBox())!.width).toBe(width);
  expect(await page.evaluate(() => localStorage.getItem("echocode:sidebar-width"))).toBe(String(width));
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  expect((await page.locator(".mh-sidebar").boundingBox())!.width).toBe(width);
  await page.reload();
  expect((await page.locator(".mh-sidebar").boundingBox())!.width).toBe(width);
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await nav.getByRole("button", { name: "外观", exact: true }).click();
  await expect(handle).toHaveAttribute("aria-valuenow", String(width));
  await handle.dblclick();
  await expect(handle).toHaveAttribute("aria-valuenow", "252");
  expect((await nav.boundingBox())!.width).toBe(252);
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  expect((await page.locator(".mh-sidebar").boundingBox())!.width).toBe(252);
});

test("maximum sidebar width fits settings and narrow overlays at manual UI scale", async ({ page }) => {
  const handle = page.getByRole("separator", { name: "调整侧栏宽度", exact: true });
  await handle.focus();
  await handle.press("End");
  for (const width of [1280, 960, 820]) {
    await page.setViewportSize({ width, height: 900 });
    if (await page.getByRole("button", { name: "展开侧栏", exact: true }).isVisible()) await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
    const nav = page.getByRole("navigation", { name: "设置分类" });
    expect((await nav.boundingBox())!.width).toBe(420);
    for (const category of ["外观", "模型与连接", "MCP 服务器", "插件", "使用统计"]) {
      await openWorkbenchPage(page, category);
      const size = await page.locator(".settings-content > .settings-page-body").evaluate(element => ({ width: element.clientWidth, scroll: element.scrollWidth }));
      expect(size.scroll, `${category} should fit with a 420px sidebar at ${width}px`).toBeLessThanOrEqual(size.width + 1);
    }
  }
  await page.evaluate(() => localStorage.setItem("echocode:ui-appearance-v1", JSON.stringify({ scaleMode: "manual", manualScale: 125 })));
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.reload();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  const nav = page.getByRole("navigation", { name: "设置分类" });
  expect((await nav.boundingBox())!.width).toBe(525);
  await nav.getByRole("button", { name: "外观", exact: true }).click();
  await expect(handle).toHaveAttribute("aria-valuenow", "420");
  await page.setViewportSize({ width: 960, height: 800 });
  await expect(page.getByRole("combobox", { name: "设置分类", exact: true })).toBeVisible();
  await expect(handle).not.toBeVisible();
  await page.setViewportSize({ width: 720, height: 800 });
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  const sidebar = page.locator(".mh-sidebar");
  expect((await sidebar.boundingBox())!.width).toBe(525);
  await page.setViewportSize({ width: 480, height: 800 });
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  const small = (await sidebar.boundingBox())!;
  const layout = await sidebar.evaluate(element => {
    const style = getComputedStyle(element);
    return { innerWidth, width: style.width, boxSizing: style.boxSizing, layoutWidth: style.getPropertyValue("--ui-layout-width"), scale: style.getPropertyValue("--ui-scale"), rail: style.getPropertyValue("--rail-width") };
  });
  expect(small.x + small.width, JSON.stringify(layout)).toBeLessThanOrEqual(480);
  const rail = (await page.getByRole("navigation", { name: "工作台导航", exact: true }).boundingBox())!;
  const titlebar = (await page.locator(".app-titlebar").boundingBox())!;
  expect(small.x).toBeCloseTo(rail.x + rail.width, 1);
  expect(small.y).toBeCloseTo(titlebar.y + titlebar.height, 1);
  expect(await page.evaluate(() => localStorage.getItem("echocode:sidebar-width"))).toBe("420");
  await page.setViewportSize({ width: 1440, height: 900 });
  expect((await sidebar.boundingBox())!.width).toBe(525);
});

test("runtime edits can be restored or explicitly saved through the shared action bar", async ({ page }) => {
  await openWorkbenchPage(page, "浏览器");
  const toggle = page.getByRole("switch", { name: "浏览器", exact: true });
  const initial = await toggle.isChecked();
  await toggle.focus();
  await toggle.press("Space");
  await expect(page.getByRole("button", { name: "保存设置", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "还原更改", exact: true }).click();
  await expect(toggle).toBeChecked({ checked: initial });
  expect(await callsFor(page, "settings")).toHaveLength(0);
  await toggle.focus();
  await toggle.press("Space");
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByRole("button", { name: "保存设置", exact: true })).toBeDisabled();
  expect((await callsFor(page, "settings"))[0].args[0].browser.enabled).toBe(!initial);
});

for (const theme of ["dark", "light"] as const) {
  test(`${theme} settings categories keep headings, controls and scrolling within the viewport`, async ({ page }, testInfo) => {
    if (theme === "light") {
      await page.getByRole("radio", { name: "浅色", exact: true }).focus();
      await page.keyboard.press("Space");
      await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    }
    const nav = page.getByRole("navigation", { name: "设置分类" });
    const categories = await nav.getByRole("button").allTextContents();
    for (const obsolete of ["常规", "个性化", "已归档对话", "自动化任务", "扩展中心"]) expect(categories.map(label => label.trim())).not.toContain(obsolete);
    for (const category of categories) {
      await nav.getByRole("button", { name: category.trim(), exact: true }).click();
      await expect(page.locator(".settings-page-head h1")).toHaveText(category.trim());
      const body = page.locator(".settings-content > .settings-page-body");
      await expect(body).toBeVisible();
      const dimensions = await body.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth }));
      expect(dimensions.scroll, `${category} has horizontal overflow`).toBeLessThanOrEqual(dimensions.client + 1);
      if (["外观", "模型与连接", "权限与执行"].includes(category.trim())) {
        await page.mouse.move(700, 40);
        await page.screenshot({ path: testInfo.outputPath(`${theme}-${category.trim()}.png`) });
      }
    }
    await page.setViewportSize({ width: 720, height: 780 });
    await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
    const mobileCategory = page.getByRole("combobox", { name: "设置分类", exact: true });
    for (const category of ["appearance", "models", "config", "browser"]) {
      await mobileCategory.selectOption(category);
      const body = page.locator(".settings-content > .settings-page-body");
      const dimensions = await body.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth }));
      expect(dimensions.scroll, `${category} has mobile horizontal overflow`).toBeLessThanOrEqual(dimensions.client + 1);
    }
    await mobileCategory.selectOption("appearance");
    await page.screenshot({ path: testInfo.outputPath(`${theme}-settings-narrow.png`) });
  });
}
