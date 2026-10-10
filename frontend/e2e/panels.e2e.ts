import { expect, test } from "@playwright/test";
import { installPanelFixture } from "./panel-fixture";
import { callsFor } from "./workbench-fixture";

const errors = new WeakMap<object, string[]>();
test.beforeEach(async ({ page }) => {
  errors.set(page, []);
  page.on("pageerror", error => errors.get(page)!.push(error.message));
  await installPanelFixture(page);
});
test.afterEach(async ({ page }) => { expect(errors.get(page)).toEqual([]); });

for (const theme of ["dark", "light"] as const) {
  test(`${theme} file tree uses soft selection and preserves keyboard navigation`, async ({ page }, testInfo) => {
    if (theme === "light") await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
    await page.getByRole("button", { name: "文件与更改", exact: true }).click();
    const tree = page.getByRole("tree", { name: "项目文件", exact: true });
    const selected = tree.getByRole("treeitem", { name: ".gitignore", exact: true });
    await expect(selected).toHaveAttribute("aria-selected", "true");
    await expect(page.locator(".cm-editor")).toBeVisible();
    await selected.click();
    const paint = await selected.evaluate(element => {
      const style = getComputedStyle(element);
      return { shadow: style.boxShadow, radius: parseFloat(style.borderRadius), background: style.backgroundColor, border: style.borderLeftWidth };
    });
    expect(paint.shadow).toBe("none");
    expect(paint.border).toBe("0px");
    expect(paint.radius).toBeGreaterThanOrEqual(6);
    expect(paint.background).not.toBe("rgba(0, 0, 0, 0)");
    const editorColors = await page.locator(".cm-editor").evaluate(element => ({ editor: getComputedStyle(element).backgroundColor, surface: getComputedStyle(element.closest(".review-diff-surface")!).backgroundColor, lineShadow: getComputedStyle(element.querySelector(".cm-activeLine")!).boxShadow }));
    expect(editorColors.editor).toBe(editorColors.surface);
    expect(editorColors.lineShadow).toBe("none");
    const folder = tree.getByRole("treeitem", { name: "src", exact: true });
    await folder.focus();
    await page.keyboard.press("ArrowRight");
    await expect(folder).toHaveAttribute("aria-expanded", "true");
    await expect(folder).toBeFocused();
    await page.keyboard.press("ArrowRight");
    const child = tree.getByRole("treeitem", { name: "main.ts", exact: true });
    await expect(child).toBeFocused();
    await expect(child).toHaveAttribute("aria-level", "2");
    await page.keyboard.press("Enter");
    await expect(child).toHaveAttribute("aria-selected", "true");
    await expect(selected).toHaveAttribute("aria-selected", "false");
    await page.keyboard.press("ArrowLeft");
    await expect(folder).toBeFocused();
    await page.keyboard.press("ArrowLeft");
    await expect(child).not.toBeVisible();
    await expect(folder).toBeFocused();
    await expect(tree.locator('[tabindex="0"]')).toHaveCount(1);
    await expect(tree.getByRole("treeitem", { name: "external-link" })).toBeDisabled();
    await selected.click();
    await page.mouse.move(400, 70);
    await page.locator(".review-panel").screenshot({ path: testInfo.outputPath(`file-panel-${theme}.png`) });
  });
}

test("file and changes tabs have keyboard navigation and linked panels", async ({ page }) => {
  await page.getByRole("button", { name: "文件与更改", exact: true }).click();
  const views = page.getByRole("tablist", { name: "文件视图", exact: true });
  await views.getByRole("tab", { name: "文件", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  const changes = views.getByRole("tab", { name: "更改", exact: true });
  await expect(changes).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(`#${await changes.getAttribute("aria-controls")}`)).toBeVisible();
  const rail = page.getByRole("tablist", { name: "项目文件与 Git 更改", exact: true });
  await rail.getByRole("tab", { name: "项目", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await page.getByRole("textbox", { name: "筛选更改", exact: true }).fill("main");
  await page.locator(".review-file-select").click();
  await expect(page.locator(".review-file-heading strong")).toHaveText("src/main.ts");
  await expect(page.locator(".review-file-select")).toHaveAttribute("aria-current", "true");
  expect(await page.locator(".review-file-row.active").evaluate(el => getComputedStyle(el).boxShadow)).toBe("none");
});

test("Git uses the shared branch selector and keyboard accessible terminal tabs", async ({ page }) => {
  await page.getByRole("button", { name: "Git 与终端", exact: true }).click();
  const branch = page.getByRole("button", { name: /^Git 分支/ });
  await branch.click();
  await page.getByRole("option", { name: "feature/panels", exact: true }).click();
  await expect(branch).toContainText("feature/panels");
  expect((await callsFor(page, "branch"))[0].args).toEqual(["feature/panels"]);
  await page.locator(".git-file-select").filter({ hasText: "src/main.ts" }).click();
  await page.getByRole("radio", { name: "工作区", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("radio", { name: "暂存区", exact: true })).toBeChecked();
  const tabs = page.getByRole("tablist", { name: "工作区工具视图", exact: true });
  await tabs.getByRole("tab", { name: /^Git/ }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(tabs.getByRole("tab", { name: "终端" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".terminal-output")).toContainText("Panel terminal ready");
});

test("workbook tabs use the same controls and fit a narrow file panel", async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 780 });
  await page.getByRole("button", { name: "文件与更改", exact: true }).click();
  const tree = page.getByRole("tree", { name: "项目文件", exact: true });
  await tree.getByRole("treeitem", { name: "docs", exact: true }).click();
  await tree.getByRole("treeitem", { name: "预算.xlsx", exact: true }).click();
  const tabs = page.getByRole("tablist", { name: "工作表", exact: true });
  await tabs.getByRole("tab", { name: "预算", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(tabs.getByRole("tab", { name: "实际", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".artifact-grid")).toContainText("80");
  const bounds = (await page.locator(".review-panel").boundingBox())!;
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(720);
  expect(await page.locator(".artifact-grid-scroll").evaluate(el => el.clientHeight)).toBeGreaterThan(100);
});

test("shared side-panel and browser tabs preserve the selected file", async ({ page }) => {
  await page.getByRole("button", { name: "文件与更改", exact: true }).click();
  await expect(page.getByRole("treeitem", { name: ".gitignore", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("button", { name: "内置浏览器", exact: true }).click();
  const browserTabs = page.getByRole("tablist", { name: "浏览器标签页", exact: true });
  await browserTabs.getByRole("tab", { name: "Documentation", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(browserTabs.getByRole("tab", { name: "Reference", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.locator(".browser-managed").evaluate(element => element.setAttribute("data-mount-check", "preserved"));
  const modes = page.getByRole("tablist", { name: "右侧面板标签", exact: true });
  await modes.getByRole("tab", { name: "浏览器", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("treeitem", { name: ".gitignore", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".review-file-heading strong")).toHaveText(".gitignore");
  await modes.getByRole("tab", { name: "浏览器", exact: true }).click();
  await expect(page.locator(".browser-managed")).toHaveAttribute("data-mount-check", "preserved");
  await expect(browserTabs.getByRole("tab", { name: "Reference", exact: true })).toHaveAttribute("aria-selected", "true");
});
