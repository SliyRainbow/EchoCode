import { expect, test } from "@playwright/test";
import { callsFor, installWorkbenchFixture, openWorkbenchPage } from "./workbench-fixture";

test.beforeEach(async ({ page }) => { await installWorkbenchFixture(page, { conversationView: "projects" }); });

test("primary sections own their navigation and settings omit duplicate destinations", async ({ page }) => {
  await openWorkbenchPage(page, "发现扩展");
  await expect(page.getByRole("navigation", { name: "扩展分类" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "设置分类" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "扩展与工具", exact: true })).toHaveAttribute("aria-pressed", "true");
  await openWorkbenchPage(page, "自动化任务");
  await expect(page.getByRole("heading", { name: "自动化任务", exact: true })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "扩展分类" })).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "设置分类" })).toHaveCount(0);
  await openWorkbenchPage(page, "外观");
  const settings = page.getByRole("navigation", { name: "设置分类" });
  await expect(settings.getByRole("button")).toHaveCount(13);
  for (const name of ["常规", "个性化", "已归档对话", "命令", "MCP 服务器", "插件", "自动化任务"]) await expect(settings.getByRole("button", { name, exact: true })).toHaveCount(0);
});

test("project picker switches the real project and protects both settings and secret drafts", async ({ page }) => {
  const picker = page.getByRole("button", { name: "切换项目", exact: true });
  await picker.click();
  await page.getByRole("menuitemradio", { name: /^Docs/ }).click();
  await expect(picker).toContainText("Docs");
  expect((await callsFor(page, "switch")).at(-1)?.args).toEqual(["project-docs", "session-docs"]);
  await openWorkbenchPage(page, "浏览器");
  const toggle = page.getByRole("switch", { name: "浏览器", exact: true });
  const initial = await toggle.isChecked();
  await toggle.focus();
  await toggle.press("Space");
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  await picker.click();
  await page.getByRole("menuitemradio", { name: /^EchoCode/ }).click();
  await expect(page.locator(".settings-page-head")).toContainText("请先保存或还原");
  await expect(toggle).toBeChecked({ checked: !initial });
  expect(await callsFor(page, "switch")).toHaveLength(1);
  await page.getByRole("button", { name: "还原更改", exact: true }).click();
  await openWorkbenchPage(page, "模型与连接");
  await page.getByLabel("模型密钥", { exact: true }).fill("draft-stays-local");
  await page.getByRole("navigation", { name: "工作台导航" }).getByRole("button", { name: "工作区", exact: true }).click();
  await picker.click();
  await page.getByRole("menuitemradio", { name: /^EchoCode/ }).click();
  await expect(page.getByLabel("模型密钥", { exact: true })).toHaveValue("draft-stays-local");
  expect(await callsFor(page, "switch")).toHaveLength(1);
});

test("conversation views persist without duplicate lists and archived filtering uses real sessions", async ({ page }) => {
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    fixture.projects[0].sessions.push({ ...fixture.projects[0].sessions[0], id: "archived", title: "已归档的历史任务", archived: true, isActive: false });
    for (let i = 0; i < 4; i++) fixture.projects[0].sessions.push({ ...fixture.projects[0].sessions[0], id: `extra-${i}`, title: `附加任务 ${i}`, isActive: false });
  });
  await page.getByRole("button", { name: "新对话", exact: true }).click();
  await page.getByRole("tab", { name: "最近", exact: true }).click();
  await expect(page.locator(".recent-session")).toHaveCount(10);
  await expect(page.locator(".proj-row")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /已归档的历史任务/ })).toHaveCount(0);
  await page.getByRole("button", { name: "显示已归档会话", exact: true }).click();
  await expect(page.locator(".recent-session")).toHaveCount(11);
  await expect(page.getByRole("button", { name: /已归档的历史任务/ })).toBeVisible();
  await page.getByRole("button", { name: "隐藏已归档会话", exact: true }).click();
  await expect(page.locator(".recent-session")).toHaveCount(10);
  await page.reload();
  await expect(page.getByRole("tab", { name: "最近", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "按项目", exact: true }).click();
  await expect(page.locator(".recent-session")).toHaveCount(0);
  await expect(page.locator(".proj-row")).toHaveCount(2);
});

test("repeated new-session shortcuts submit only once until creation finishes", async ({ page }) => {
  await page.evaluate(() => { (window as any).__workbenchTest.uiControls.newSessionDelay = 500; });
  await page.keyboard.press("Control+n");
  await page.keyboard.press("Control+n");
  await page.keyboard.press("Control+n");
  await expect.poll(async () => (await callsFor(page, "new-session")).length).toBe(1);
  await expect(page.getByRole("button", { name: "新对话", exact: true })).toBeEnabled();
  expect(await callsFor(page, "new-session")).toHaveLength(1);
});

test("automation loading failures are visible and live updates preserve edited drafts", async ({ page }) => {
  await page.evaluate(() => { (window as any).__workbenchTest.uiControls.automationError = "自动化文件暂不可读"; });
  await openWorkbenchPage(page, "自动化任务");
  await expect(page.getByText("任务列表暂不可用", { exact: true })).toBeVisible();
  await expect(page.locator(".automation-error")).toContainText("自动化文件暂不可读");
  await page.getByRole("button", { name: "工作区", exact: true }).click();
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    fixture.uiControls.automationError = "";
    fixture.automation.tasks = [{ id: "audit", name: "每日检查", prompt: "检查项目", projectId: "project-echo", sessionId: "session-new", enabled: false, schedule: { kind: "interval", intervalMinutes: 60 }, runCount: 1, failureCount: 0, lastRun: { status: "completed", message: "已完成" } }];
  });
  await openWorkbenchPage(page, "自动化任务");
  const name = page.getByPlaceholder("例如：每日检查主分支");
  await name.fill("保留手动编辑");
  const save = page.getByRole("button", { name: "保存任务", exact: true });
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    fixture.automation.tasks[0].lastRun = { status: "running", message: "正在检查" };
    fixture.emit("automation:state", fixture.automation);
  });
  await expect(name).toHaveValue("保留手动编辑");
  await expect(save).toBeDisabled();
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    fixture.automation.tasks[0].lastRun = { status: "completed", message: "第二轮完成" };
    fixture.automation.tasks[0].runCount = 2;
    fixture.emit("automation:state", fixture.automation);
    fixture.uiControls.automationSaveError = "写入自动化设置失败";
  });
  await expect(save).toBeEnabled();
  await expect(name).toHaveValue("保留手动编辑");
  await expect(page.locator(".automation-last-run")).toContainText("第二轮完成");
  await save.click();
  await expect(page.locator(".automation-error")).toContainText("写入自动化设置失败");
  await expect(name).toHaveValue("保留手动编辑");
});

test("empty model routes display their label and can be reselected", async ({ page }) => {
  await openWorkbenchPage(page, "自动化任务");
  const route = page.getByRole("button", { name: /^模型路由/ });
  await expect(route).toContainText("跟随当前模型");
  await route.click();
  await page.getByRole("option", { name: "DeepSeek 官方", exact: true }).click();
  await expect(route).toContainText("DeepSeek 官方");
  await route.click();
  await page.getByRole("option", { name: "跟随当前模型", exact: true }).click();
  await expect(route).toContainText("跟随当前模型");
  await page.getByRole("button", { name: "保存任务", exact: true }).click();
  expect((await callsFor(page, "automation-save"))[0].args[0].providerId).toBe("");
});

test("history persistence failures keep the visible answer when changing sessions", async ({ page }) => {
  await page.getByRole("tab", { name: "最近", exact: true }).click();
  const editor = page.getByRole("textbox", { name: "向 EchoCode 提问，或描述要修改的代码" });
  await editor.fill("检查历史保存");
  await editor.press("Enter");
  await expect.poll(async () => (await callsFor(page, "send")).length).toBe(1);
  await page.evaluate(() => {
    const fixture = (window as any).__workbenchTest;
    const task: any = [...fixture.tasks.values()][0];
    fixture.tasks.delete(task.taskId);
    fixture.emit("chat:task", { ...task, type: "failed", message: "磁盘写入失败", result: { content: "这份实际输出需要保留", model: "test", state: fixture.state, turnCommitted: true, historyError: "本轮结果未能完整保存：磁盘写入失败" } });
  });
  await expect(page.locator(".workbench-notice")).toContainText("未能完整保存");
  await expect(page.locator(".op-msg.assistant")).toContainText("这份实际输出需要保留");
  await page.locator(".recent-session").filter({ hasText: "检查 API 错误处理" }).click();
  await page.locator(".recent-session").filter({ hasText: "新对话" }).click();
  await expect(page.locator(".op-msg.assistant")).toContainText("这份实际输出需要保留");
});
