import { openWorkbenchPage } from "./workbench-fixture";
import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture } from "./workbench-fixture";

const errors = new WeakMap<object, string[]>();
test.beforeEach(async ({ page }) => {
  errors.set(page, []);
  page.on("pageerror", error => errors.get(page)!.push(error.message));
  await installWorkbenchFixture(page, { audit: true });
  await page.getByRole("button", { name: "设置", exact: true }).click();
});
test.afterEach(async ({ page }) => { expect(errors.get(page)).toEqual([]); });

test("MCP selection, edits and reconnection preserve the server identity", async ({ page }) => {
  await openWorkbenchPage(page, "MCP 服务器");
  await page.locator(".mcp-server-option").filter({ hasText: "仓库分析与文档检索" }).click();
  await page.getByRole("textbox", { name: "服务器名称", exact: true }).fill("审查服务");
  await page.getByRole("textbox", { name: "启动命令", exact: true }).fill("fixture-runner");
  await expect(page.getByRole("button", { name: "保存设置", exact: true })).toHaveCount(1);
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  const saved = (await callsFor(page, "settings"))[0].args[0];
  const server = saved.mcp.servers.find((item: any) => item.id === "repository-analysis");
  expect(server.name).toBe("审查服务");
  expect(server.command).toBe("fixture-runner");
  await page.getByRole("button", { name: "保存配置并重新连接", exact: true }).click();
  expect((await callsFor(page, "mcp-refresh"))[0].args).toEqual(["repository-analysis"]);
  const body = (await page.locator(".settings-content > .settings-page-body").boundingBox())!;
  const footer = (await page.locator(".settings-save-footer").boundingBox())!;
  expect(body.y + body.height).toBeLessThanOrEqual(footer.y + 1);
  await page.locator(".mcp-server-option").filter({ hasText: "filesystem" }).click();
  await expect(page.getByRole("button", { name: /^连接方式/ })).toBeDisabled();
  await expect(page.getByRole("button", { name: "删除 MCP 服务器", exact: true })).toHaveCount(0);
});

test("plugin switches, permissions and confirmations target the selected plugin", async ({ page }) => {
  await openWorkbenchPage(page, "插件");
  await page.locator(".plugin-list-option").filter({ hasText: "仓库质量检查" }).click();
  await page.getByRole("switch", { name: "启用 仓库质量检查", exact: true }).focus();
  await page.keyboard.press("Space");
  await page.getByRole("switch", { name: "修改工作区文件", exact: true }).focus();
  await page.keyboard.press("Space");
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  const settings = (await callsFor(page, "settings"))[0].args[0];
  const plugin = settings.plugins.entries.find((item: any) => item.id === "repository-quality");
  expect(plugin.enabled).toBe(true);
  expect(plugin.permissions.fileWrite).toBe(true);
  await page.getByRole("button", { name: "打开目录", exact: true }).click();
  expect((await callsFor(page, "plugin-reveal"))[0].args).toEqual(["repository-quality"]);
  await page.getByRole("button", { name: "卸载插件", exact: true }).click();
  const confirmation = page.getByRole("dialog");
  await confirmation.getByRole("button", { name: "取消", exact: true }).click();
  expect(await callsFor(page, "plugin-uninstall")).toHaveLength(0);
  await page.getByRole("button", { name: "卸载插件", exact: true }).click();
  await confirmation.getByRole("button", { name: "卸载", exact: true }).click();
  expect((await callsFor(page, "plugin-uninstall"))[0].args).toEqual(["repository-quality"]);
  await expect(page.locator(".plugin-list-option").filter({ hasText: "仓库质量检查" })).toHaveCount(0);
});

test("usage distinguishes cumulative, session and latest-response data", async ({ page }) => {
  await openWorkbenchPage(page, "使用统计");
  await expect(page.getByRole("heading", { name: "使用统计", exact: true })).toHaveCount(1);
  await expect(page.locator(".usage-summary")).toContainText("1,200,000");
  await expect(page.locator(".usage-summary")).toContainText("60,000");
  await expect(page.locator(".usage-summary")).toContainText("$4.80");
  const session = page.getByRole("region", { name: "当前会话用量" });
  await expect(session).toContainText("97,000");
  await expect(session).toContainText("60,000");
  await expect(session).toContainText("15,000");
  await expect(page.getByRole("meter", { name: "会话缓存命中率" })).toHaveAttribute("aria-valuenow", "80");
  const track = (await session.locator(".usage-meter-track").boundingBox())!;
  const fill = (await session.locator(".usage-meter-fill").boundingBox())!;
  expect(fill.width / track.width).toBeCloseTo(0.8, 2);
  await page.getByRole("tab", { name: "诊断", exact: true }).click();
  const diagnostics = page.getByRole("tabpanel", { name: "诊断", exact: true });
  await expect(diagnostics).toContainText("90.0%");
  await expect(diagnostics).toContainText("9,000");
  await expect(diagnostics).toContainText("1,000");
  await expect(page.locator(".settings-save-footer")).toHaveCount(0);
});

test("billing filters, manual reconciliation and failures stay visible in settings", async ({ page }) => {
  await openWorkbenchPage(page, "使用统计");
  await page.getByRole("tab", { name: "账单对账", exact: true }).click();
  await page.getByRole("button", { name: /^账单供应商/ }).click();
  await page.getByRole("option", { name: "OpenAI 官方（测试）", exact: true }).click();
  await page.getByLabel("账单开始日期", { exact: true }).fill("2026-10-01");
  await page.getByLabel("账单结束日期", { exact: true }).fill("2026-10-09");
  await page.getByRole("button", { name: "同步官方账单", exact: true }).click();
  expect((await callsFor(page, "billing-sync"))[0].args[0].providerId).toBe("audit-openai");
  await expect(page.getByRole("tabpanel", { name: "账单对账" })).toContainText("$1.25");
  await expect(page.getByRole("button", { name: "保存对账", exact: true })).toBeDisabled();
  await page.getByRole("spinbutton", { name: "官方金额（USD）", exact: true }).fill("1.50");
  await page.getByRole("textbox", { name: "对账备注", exact: true }).fill("界面测试");
  await page.getByRole("button", { name: "保存对账", exact: true }).click();
  expect((await callsFor(page, "billing-reconcile"))[0].args[0].officialCost).toBe(1.5);
  await page.evaluate(() => { (window as any).__workbenchTest.uiControls.billingError = "账单读取失败（测试）"; });
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(1);
  await expect(page.getByRole("alert")).toContainText("账单读取失败（测试）");
  await page.getByRole("button", { name: "关闭设置错误提示", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("settings keep keyboard focus out of the covered workbench and expose save errors", async ({ page }) => {
  await openWorkbenchPage(page, "浏览器");
  expect(await page.locator(".workbench-main").evaluate(element => (element as HTMLElement).inert)).toBe(true);
  expect(await page.locator(".mh-sidebar").evaluate(element => (element as HTMLElement).inert)).toBe(true);
  await page.getByRole("switch", { name: "浏览器", exact: true }).focus();
  await page.keyboard.press("Space");
  await page.evaluate(() => { (window as any).__workbenchTest.uiControls.saveError = "保存配置失败（测试）"; });
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("保存配置失败（测试）");
  await expect(page.getByRole("button", { name: "保存设置", exact: true })).toBeEnabled();
});
