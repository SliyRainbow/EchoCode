import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture } from "./workbench-fixture";

const pageErrors = new WeakMap<object, string[]>();
test.beforeEach(async ({ page }) => {
  const errors: string[] = [];
  pageErrors.set(page, errors);
  page.on("pageerror", (error) => errors.push(error.message));
  await installWorkbenchFixture(page);
});
test.afterEach(async ({ page }) => { expect(pageErrors.get(page)).toEqual([]); });

test("menus support keyboard selection, close with Escape and restore focus", async ({ page }) => {
  const trigger = page.getByRole("button", { name: "选择推理强度", exact: true });
  await trigger.focus();
  await page.keyboard.press("Enter");
  const menu = page.getByRole("menu", { name: /推理强度/ });
  await expect(menu).toBeVisible();
  await page.keyboard.press("Home");
  await page.keyboard.press("Enter");
  await expect(menu).not.toBeVisible();
  expect((await callsFor(page, "reasoning")).length).toBe(1);
  await expect(trigger).toBeFocused();
  await trigger.click();
  await expect(menu).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
  await page.getByRole("button", { name: "选择模型", exact: true }).click();
  const providers = page.getByRole("menu", { name: "选择模型", exact: true });
  await expect(providers).toBeVisible();
  await expect(page.getByRole("menuitemradio").first()).toBeVisible();
  await page.getByRole("menuitemradio").last().click();
  await expect(providers).not.toBeVisible();
  expect((await callsFor(page, "settings")).length).toBeGreaterThan(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`${theme} reasoning options keep full-width text and checks at the right edge`, async ({ page }, testInfo) => {
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    const trigger = page.getByRole("button", { name: "选择推理强度", exact: true });
    await trigger.click();
    await page.getByRole("menuitemradio", { name: /^很高/ }).click();
    for (const width of [1440, 480]) {
      await page.setViewportSize({ width, height: 900 });
      await trigger.click();
      const menu = page.locator(".reasoning-options");
      const selected = menu.getByRole("menuitemradio", { checked: true });
      await expect(selected).toContainText("很高");
      const rows = await menu.getByRole("menuitemradio").evaluateAll(elements => elements.map(element => {
        const row = element.getBoundingClientRect();
        const text = element.querySelector(".ui-menu-copy")!.getBoundingClientRect();
        const check = element.querySelector(".ui-menu-indicator")?.getBoundingClientRect();
        return { rowWidth: row.width, textWidth: text.width, rightInset: check ? row.right - check.right : null, gap: check ? check.left - text.right : null };
      }));
      const textWidths = rows.map(row => row.textWidth);
      expect(Math.max(...textWidths) - Math.min(...textWidths)).toBeLessThan(1);
      for (const row of rows) {
        expect(row.textWidth).toBeGreaterThan(row.rowWidth * .75);
        if (row.rightInset !== null) {
          expect(row.rightInset).toBeGreaterThanOrEqual(8);
          expect(row.rightInset).toBeLessThanOrEqual(12);
          expect(row.gap).toBeGreaterThanOrEqual(8);
        }
      }
      const bounds = (await menu.boundingBox())!;
      expect(bounds.x).toBeGreaterThanOrEqual(0);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
      if (width === 1440) {
        await page.mouse.move(700, 40);
        await menu.screenshot({ path: testInfo.outputPath(`${theme}-reasoning-menu.png`) });
      }
      await page.keyboard.press("Escape");
      await expect(trigger).toBeFocused();
    }
  });
}

test("search finds sessions by name and switches through the Wails boundary", async ({ page }) => {
  await page.keyboard.press("Control+k");
  const search = page.getByRole("combobox", { name: "搜索对话与项目" });
  await expect(search).toBeFocused();
  await search.fill("API");
  await expect(page.getByRole("option")).toHaveCount(1);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(page.locator(".chat-breadcrumb")).toContainText("检查 API 错误处理");
  expect((await callsFor(page, "switch")).at(-1)?.args).toEqual(["project-echo", "session-api"]);
  await page.keyboard.press("Control+k");
  await search.fill("不存在的会话");
  await expect(page.getByText("没有找到匹配的对话或项目")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
});

test("composer suggestions, attachment menu and undo/redo preserve draft input", async ({ page }) => {
  await page.getByRole("button", { name: "检查最近的代码改动" }).click();
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await expect(editor).toHaveValue("检查当前工作区未提交的修改并指出风险");
  const actions = page.getByRole("button", { name: "添加附件与输入操作" });
  await actions.click();
  await page.getByRole("menuitem", { name: /撤销输入/ }).click();
  await expect(editor).toHaveValue("");
  await actions.click();
  await page.getByRole("menuitem", { name: /重做输入/ }).click();
  await expect(editor).toHaveValue("检查当前工作区未提交的修改并指出风险");
  await actions.click();
  const chooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("menuitem", { name: "添加 Markdown 资料" }).click();
  await (await chooserPromise).setFiles({ name: "reference.md", mimeType: "text/markdown", buffer: Buffer.from("# 开发说明\n保持现有接口兼容。") });
  await expect(page.locator(".composer-attachments")).toContainText("reference.md");
});

test("send, stream, background completion and stop stay bound to the right session", async ({ page }) => {
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await editor.fill("请检查当前修改");
  await editor.press("Enter");
  await expect(page.locator(".op-msg.assistant")).toContainText("我正在检查工作区的改动。");
  const firstSend = (await callsFor(page, "send"))[0];
  expect(firstSend.args.slice(0, 3)).toEqual(["project-echo", "session-new", "请检查当前修改"]);
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await expect(page.locator(".chat-breadcrumb")).toContainText("检查 API 错误处理");
  await expect(page.locator(".op-msg.assistant")).toHaveCount(0);
  await page.evaluate(() => { const bridge = (window as any).__workbenchTest; bridge.complete([...bridge.tasks.keys()][0]); });
  await expect(page.locator(".chat-breadcrumb")).toContainText("检查 API 错误处理");
  await editor.fill("继续检查接口");
  await editor.press("Enter");
  await expect(page.locator(".op-msg.assistant")).toContainText("我正在检查工作区的改动。");
  await page.getByRole("button", { name: "停止生成", exact: true }).click();
  await expect(page.getByRole("button", { name: "停止生成", exact: true })).not.toBeVisible();
  expect(await callsFor(page, "stop")).toHaveLength(1);
  await page.locator(".recent-session").filter({ hasText: "新对话" }).click();
  await expect(page.locator(".op-msg.assistant")).toContainText("已完成代码审阅。");
});

test("approval dialog traps focus and Escape rejects the pending request", async ({ page }) => {
  await page.evaluate(() => (window as any).__workbenchTest.emit("approval:request", { id: "approval-test", tool: "run_command", kind: "command", command: "go test ./...", summary: "运行项目测试" }));
  const dialog = page.getByRole("dialog", { name: "命令执行请求" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "拒绝", exact: true })).toBeFocused();
  for (let index = 0; index < 5; index++) await page.keyboard.press("Tab");
  expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  await page.evaluate(() => (window as any).__workbenchTest.emit("approval:request", { id: "approval-next", tool: "run_command", kind: "command", command: "bun run check", summary: "检查前端" }));
  await page.keyboard.press("Escape");
  expect((await callsFor(page, "approval"))[0].args).toEqual(["approval-test", "run_command", false, "once"]);
  await expect(dialog).toContainText("检查前端");
  await expect(dialog.getByRole("button", { name: "拒绝", exact: true })).toBeFocused();
  await dialog.getByRole("button", { name: "拒绝", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  expect((await callsFor(page, "approval"))[1].args).toEqual(["approval-next", "run_command", false, "once"]);
});

test("task modes keep their state and become read-only while a task is running", async ({ page }) => {
  const trigger = page.getByRole("button", { name: "任务模式与权限", exact: true });
  await trigger.click();
  await page.getByRole("menuitemcheckbox", { name: "先规划，再执行" }).click();
  await expect(page.getByRole("menuitemcheckbox", { name: "先规划，再执行" })).toBeChecked();
  expect((await callsFor(page, "plan"))[0].args).toEqual([true]);
  await page.keyboard.press("Escape");
  await expect(trigger).toContainText("先规划");
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await editor.fill("分析当前模块");
  await editor.press("Enter");
  await expect(page.getByRole("button", { name: "停止生成", exact: true })).toBeEnabled();
  await trigger.click();
  await expect(page.getByRole("menuitemcheckbox", { name: "先规划，再执行" })).toBeDisabled();
  await expect(page.getByRole("menuitemradio", { name: "完全访问", exact: true })).toBeDisabled();
});

test("file preview and settings remain reachable in a small window", async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 780 });
  if (!(await page.locator(".mh-shell").getAttribute("class"))?.includes("sidebar-collapsed")) await page.keyboard.press("Control+b");
  await page.getByRole("button", { name: "文件与更改", exact: true }).click();
  await expect(page.getByRole("button", { name: "关闭文件面板" })).toBeVisible();
  const panel = (await page.locator(".side-panel-host").boundingBox())!;
  expect(panel.x).toBeGreaterThanOrEqual(0);
  expect(panel.x + panel.width).toBeLessThanOrEqual(720);
  await page.getByRole("button", { name: "关闭文件面板" }).click();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await page.getByRole("combobox", { name: "设置分类", exact: true }).selectOption("browser");
  const toggle = page.getByRole("switch").first();
  const checked = await toggle.isChecked();
  await toggle.focus();
  await page.keyboard.press("Space");
  await expect(toggle).toBeChecked({ checked: !checked });
  await page.getByRole("combobox", { name: "设置分类", exact: true }).selectOption("appearance");
  await expect(page.getByRole("heading", { name: "外观", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("destructive confirmation defaults to cancel and only commits after confirmation", async ({ page }) => {
  await page.getByRole("tab", { name: "按项目", exact: true }).click();
  await page.locator(".proj-row").filter({ hasText: "EchoCode" }).click();
  await page.locator(".sess-row-wrap").first().hover();
  await page.getByRole("button", { name: "永久删除对话", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "取消", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  expect(await callsFor(page, "delete")).toHaveLength(0);
  await page.locator(".sess-row-wrap").first().hover();
  await page.getByRole("button", { name: "永久删除对话", exact: true }).first().click();
  await dialog.locator(".primary").click();
  await expect(dialog).not.toBeVisible();
  expect(await callsFor(page, "delete")).toHaveLength(1);
});

test("sidebar resizing accounts for the navigation rail and keyboard collapse", async ({ page }) => {
  const sidebar = page.locator(".mh-sidebar");
  const before = (await sidebar.boundingBox())!;
  const handle = (await page.getByRole("separator", { name: "调整侧栏宽度" }).boundingBox())!;
  await page.mouse.move(handle.x + handle.width / 2, handle.y + 150);
  await page.mouse.down();
  await page.mouse.move(handle.x + handle.width / 2 + 40, handle.y + 150);
  await page.mouse.up();
  expect(Math.abs((await sidebar.boundingBox())!.width - before.width - 40)).toBeLessThan(1);
  await page.keyboard.press("Control+b");
  await expect(sidebar).not.toBeVisible();
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await expect(sidebar).toBeVisible();
});

for (const theme of ["dark", "light"] as const) {
  test(`${theme} workbench remains usable at desktop and narrow widths`, async ({ page }, testInfo) => {
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await page.mouse.move(700, 40);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-desktop.png`) });
    for (const width of [1100, 850, 720, 480]) {
      await page.setViewportSize({ width, height: 780 });
      if (!(await page.locator(".mh-shell").getAttribute("class"))?.includes("sidebar-collapsed")) await page.keyboard.press("Control+b");
      const composer = (await page.locator(".composer-box").boundingBox())!;
      expect(composer.height).toBeLessThan(200);
      expect((await page.locator(".composer-toolbar").boundingBox())!.height).toBeLessThan(50);
      expect(composer.x).toBeGreaterThanOrEqual(0);
      expect(composer.x + composer.width).toBeLessThanOrEqual(width);
      const send = (await page.getByRole("button", { name: "发送", exact: true }).boundingBox())!;
      expect(send.x + send.width).toBeLessThanOrEqual(width);
      await page.getByRole("button", { name: "选择推理强度", exact: true }).click();
      const menu = page.getByRole("menu", { name: /推理强度/ });
      await expect(menu).toBeVisible();
      const bounds = (await menu.boundingBox())!;
      expect(bounds.x).toBeGreaterThanOrEqual(0);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
      await page.keyboard.press("Escape");
    }
    await page.screenshot({ path: testInfo.outputPath(`${theme}-narrow.png`) });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "外观", exact: true })).toBeVisible();
    await page.mouse.move(700, 40);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-settings.png`) });
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
  });
}
