import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

const errors = new WeakMap<object, string[]>();
test.beforeEach(async ({ page }) => {
  errors.set(page, []);
  page.on("pageerror", (error) => errors.get(page)!.push(error.message));
  await installWorkbenchFixture(page);
  await page.getByRole("button", { name: "扩展与工具", exact: true }).click();
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).toBeVisible();
});
test.afterEach(async ({ page }) => { expect(errors.get(page)).toEqual([]); });

test("catalog tabs support keyboard navigation and searching never leaves stale details", async ({ page }) => {
  const recommended = page.getByRole("tab", { name: /^推荐/ });
  await recommended.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: /^已安装/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).not.toBeVisible();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: /^MCP/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel")).toHaveAccessibleName(/^MCP/);
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).toBeVisible();
  const search = page.getByRole("textbox", { name: "搜索扩展", exact: true });
  await search.fill("不存在的扩展");
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).not.toBeVisible();
  await expect(page.getByText("没有符合条件的扩展", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "清除扩展搜索" }).click();
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).toBeVisible();
  await search.fill("调用链");
  await expect(page.getByRole("button", { name: "CodeGraph", exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.evaluate(() => { (window as any).__workbenchTest.catalogControls.error = "目录暂时无法连接"; });
  await page.getByRole("button", { name: "刷新扩展目录" }).click();
  await expect(page.getByRole("alert")).toContainText("目录暂时无法连接");
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).toBeVisible();
});

test("install, project actions, updates and uninstall preserve host confirmations", async ({ page }) => {
  const install = page.getByRole("button", { name: "安装", exact: true });
  await install.click();
  const confirmation = page.getByRole("dialog");
  await expect(confirmation).toContainText("读取已授权项目");
  expect(await callsFor(page, "extension-install")).toHaveLength(0);
  await confirmation.getByRole("button", { name: "取消", exact: true }).click();
  expect(await callsFor(page, "extension-install")).toHaveLength(0);
  await install.click();
  await confirmation.getByRole("button", { name: "安装", exact: true }).click();
  await expect(page.getByRole("button", { name: "卸载", exact: true })).toBeVisible();
  expect((await callsFor(page, "extension-install"))[0].args).toEqual(["mcp.codegraph"]);
  await page.getByRole("button", { name: "打开目录", exact: true }).click();
  expect((await callsFor(page, "extension-reveal"))[0].args).toEqual(["mcp.codegraph"]);
  await page.getByRole("button", { name: "检查索引", exact: true }).click();
  expect((await callsFor(page, "extension-action"))[0].args).toEqual(["mcp.codegraph", "index-status"]);
  await page.getByText("检查索引完成", { exact: true }).click();
  await expect(page.getByText("索引状态：就绪", { exact: true })).toBeVisible();
  await page.evaluate(() => {
    const extension = (window as any).__workbenchTest.catalog.packages[0];
    extension.updateAvailable = true;
    extension.manifest.version = "1.5.1";
  });
  await page.getByRole("button", { name: "刷新扩展目录" }).click();
  await page.getByRole("button", { name: "更新", exact: true }).click();
  await confirmation.getByRole("button", { name: "更新", exact: true }).click();
  await expect(page.getByRole("button", { name: "更新", exact: true })).not.toBeVisible();
  expect(await callsFor(page, "extension-install")).toHaveLength(2);
  await page.getByRole("tab", { name: /^已安装/ }).click();
  await page.getByRole("button", { name: "卸载", exact: true }).click();
  expect(await callsFor(page, "extension-uninstall")).toHaveLength(0);
  await confirmation.getByRole("button", { name: "卸载", exact: true }).click();
  expect((await callsFor(page, "extension-uninstall"))[0].args).toEqual(["mcp.codegraph"]);
  await expect(page.getByRole("heading", { name: "CodeGraph", exact: true })).not.toBeVisible();
  await expect(page.getByText("没有符合条件的扩展", { exact: true })).toBeVisible();
});

for (const theme of ["dark", "light"] as const) {
  test(`${theme} settings use the entire wide viewport at normal and reduced UI scale`, async ({ page }, testInfo) => {
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    for (const width of [1920, 2560]) {
      await page.setViewportSize({ width, height: 1080 });
      for (const category of ["外观", "模型与连接", "发现扩展", "MCP 服务器", "插件", "使用统计", "自动化任务"]) {
        await openWorkbenchPage(page, category);
        await expect(page.locator(".settings-page-head h1")).toHaveText(category);
        const content = (await page.locator(".settings-content").boundingBox())!;
        const body = (await page.locator(".settings-content > .settings-page-body").boundingBox())!;
        const header = (await page.locator(".settings-page-head").boundingBox())!;
        expect(Math.abs(body.x + body.width - content.x - content.width), `${category} body leaves unused width`).toBeLessThan(2);
        expect(Math.abs(header.width - content.width), `${category} header stays fixed width`).toBeLessThan(2);
      }
      await openWorkbenchPage(page, "发现扩展");
      const list = (await page.getByRole("region", { name: "扩展列表" }).boundingBox())!;
      expect(list.width).toBeLessThanOrEqual(312);
      const details = (await page.getByRole("region", { name: "扩展详情" }).boundingBox())!;
      const toolbar = (await page.locator(".catalog-toolbar").boundingBox())!;
      expect(Math.abs(details.x + details.width - toolbar.x - toolbar.width)).toBeLessThan(2);
      const info = (await page.getByRole("complementary", { name: "版本与来源" }).boundingBox())!;
      const main = (await page.locator(".catalog-main-sections").boundingBox())!;
      expect(info.x).toBeGreaterThan(main.x + main.width);
      if (width === 1920) {
        await page.mouse.move(900, 35);
        await page.screenshot({ path: testInfo.outputPath(`${theme}-extensions-wide.png`) });
      }
    }
    await page.evaluate(() => localStorage.setItem("echocode:ui-appearance-v1", JSON.stringify({ scaleMode: "manual", manualScale: 80 })));
    await page.reload();
    await page.getByRole("button", { name: "扩展与工具", exact: true }).click();
    const content = (await page.locator(".settings-content").boundingBox())!;
    const body = (await page.locator(".settings-content > .settings-page-body").boundingBox())!;
    expect(Math.abs(body.x + body.width - content.x - content.width)).toBeLessThan(2);
    await page.mouse.move(900, 35);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-extensions-80-percent.png`) });
    await page.setViewportSize({ width: 480, height: 780 });
    const list = (await page.getByRole("region", { name: "扩展列表" }).boundingBox())!;
    const details = (await page.getByRole("region", { name: "扩展详情" }).boundingBox())!;
    expect(details.y).toBeGreaterThan(list.y + list.height);
    const overflow = await page.locator(".settings-content > .settings-page-body").evaluate((element) => element.scrollWidth - element.clientWidth);
    expect(overflow).toBeLessThanOrEqual(1);
  });
}
