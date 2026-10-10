import type { Page } from "@playwright/test";
import { installWorkbenchFixture } from "./workbench-fixture";

export async function installPanelFixture(page: Page) {
  await installWorkbenchFixture(page);
  await page.evaluate(() => {
    const test = (window as any).__workbenchTest;
    const app = (window as any).go.main.App;
    const record = (name: string, ...args: unknown[]) => test.calls.push({ name, args });
    const entry = (path: string, directory = false, link = false) => ({ path, name: path.split("/").at(-1), isDirectory: directory, isSymlink: link, size: directory ? 0 : 128 });
    const entries: Record<string, any[]> = {
      "": [entry("src", true), entry("docs", true), entry(".gitignore"), entry("README.md"), entry("a-long-file-name-that-stays-inside-the-project-sidebar.ts"), entry("external-link", false, true)],
      src: [entry("src/main.ts"), entry("src/styles.css")],
      docs: [entry("docs/预算.xlsx")],
    };
    const files = [
      { path: ".gitignore", indexStatus: ".", worktreeStatus: "M", staged: false, modified: true, untracked: false, conflicted: false },
      { path: "src/main.ts", indexStatus: "M", worktreeStatus: "M", staged: true, modified: true, untracked: false, conflicted: false },
    ];
    const git = { available: true, branch: "main", root: "C:\\work\\EchoCode", clean: false, files, branches: [{ name: "main", current: true }, { name: "feature/panels", current: false }], stagedCount: 1, modifiedCount: 2, untrackedCount: 0, ahead: 0, behind: 0 };
    const patch = "diff --git a/.gitignore b/.gitignore\n--- a/.gitignore\n+++ b/.gitignore\n@@ -1 +1,2 @@\n node_modules/\n+build/\n";
    const terminal = { id: "panel-terminal", shell: "PowerShell", workdir: "C:\\work\\EchoCode", running: true, output: "Panel terminal ready\n", startedAt: new Date().toISOString(), sandboxed: true, privilegeRestricted: true };
    const browser = { available: true, running: true, engine: "Test browser", renderMode: "stream", activeTabId: "docs", cdpEnabled: true, downloads: [], tabs: [{ id: "docs", title: "Documentation", url: "https://example.com/docs", loading: false }, { id: "reference", title: "Reference", url: "https://example.com/reference", loading: false }] };
    Object.assign(app, {
      GetGitStatus: async () => structuredClone(git),
      GetGitDiff: async (path: string, staged: boolean) => ({ path, staged, patch, truncated: false }),
      GetGitReviewDiff: async (path: string, staged: boolean) => ({ path, staged, patch, truncated: false }),
      SwitchGitBranch: async (branch: string) => { record("branch", branch); git.branch = branch; return structuredClone(git); },
      ListWorkspaceDirectory: async (path: string) => ({ path, entries: entries[path] ?? [], truncated: false }),
      ReadWorkspaceFile: async (path: string) => {
        record("file-read", path);
        const content = path === ".gitignore" ? "# Build output\nnode_modules/\nbuild/\n\n# Local settings\n.env\n" : `// ${path}\nexport const message = \"EchoCode\";\n`;
        return { path, name: path.split("/").at(-1), content, encoding: "utf-8", lineEnding: "lf", lineCount: content.split("\n").length, size: content.length, binary: path.endsWith(".xlsx"), tooLarge: false, truncated: false,
          ...(path.endsWith(".xlsx") ? { artifact: { kind: "spreadsheet", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", spreadsheet: { activeSheet: "预算", truncated: false, sheets: [{ name: "预算", rows: [["项目", "预算"], ["设计", "100"]], rowCount: 2, columnCount: 2, truncated: false }, { name: "实际", rows: [["项目", "实际"], ["设计", "80"]], rowCount: 2, columnCount: 2, truncated: false }] } } } : {}),
        };
      },
      StartTerminalSession: async () => structuredClone(terminal),
      GetTerminalSession: async () => structuredClone(terminal),
      StopTerminalSession: async () => { terminal.running = false; },
      OpenBrowserURL: async () => structuredClone(browser),
      GetBrowserState: async () => structuredClone(browser),
      BrowserActivateTab: async (id: string) => { record("browser-tab", id); browser.activeTabId = id; return structuredClone(browser); },
      BrowserCloseTab: async (id: string) => { browser.tabs = browser.tabs.filter(tab => tab.id !== id); if (browser.activeTabId === id) browser.activeTabId = browser.tabs[0]?.id ?? ""; return structuredClone(browser); },
      BrowserResize: async () => {},
      BrowserHideNativeSurface: async () => {},
      GetBrowserFrame: async (id: string) => ({ tab: browser.tabs.find(tab => tab.id === id), width: 640, height: 360, imageDataUrl: "data:image/svg+xml;base64," + btoa('<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360"><rect width="640" height="360" fill="#fafafa"/></svg>'), elements: [], capturedAt: new Date().toISOString() }),
      GetBrowserInspector: async (id: string) => ({ snapshot: { title: browser.tabs.find(tab => tab.id === id)?.title, url: "https://example.com", text: "Fixture page", elements: [], capturedAt: new Date().toISOString() }, console: [], network: [] }),
    });
  });
}
