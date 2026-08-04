import { describe, expect, test } from "bun:test";

import { hasMeaningfulTurnOutput, hasUsablePartialResult } from "../src/lib/chat-results";

describe("chat UI regressions", () => {
  test("provides a managed CodeGraph preset and Chinese activity timeline", async () => {
    const [panels, messageContent] = await Promise.all([
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
    ]);
    expect(panels).toContain('title="添加 CodeGraph 预设"');
    expect(panels).toContain('{ key: "CODEGRAPH_NO_DAEMON", value: "1" }');
    expect(panels).toContain('{ key: "CODEGRAPH_TELEMETRY", value: "0" }');
    expect(panels).toContain('args: ["serve", "--mcp"]');
    expect(messageContent).toContain('return "codegraph"');
    expect(messageContent).toContain('"正在分析代码关系"');
    expect(messageContent).toContain('return "代码关系分析"');
  });

  test("uses a theme-aware frameless title bar with complete window controls", async () => {
    const [main, app, titlebar, workbench, css] = await Promise.all([
      Bun.file(new URL("../../main.go", import.meta.url)).text(),
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/AppTitleBar.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(main).toContain("Frameless: true");
    expect(main).toContain("DisableFramelessWindowDecorations: false");
    expect(app).toContain("<AppTitleBar />");
    expect(titlebar).toContain("WindowMinimise");
    expect(titlebar).toContain("WindowToggleMaximise");
    expect(titlebar).toContain("WindowIsMaximised");
    expect(titlebar).toContain("Quit");
    expect(titlebar).toContain("window as Window & { runtime?: DesktopRuntime }");
    expect(titlebar).not.toContain("../../wailsjs/runtime/runtime");
    expect(css).toContain("--wails-draggable: drag");
    expect(css).toContain("grid-template-rows: var(--app-titlebar-height) minmax(0, 1fr)");
    expect(css).toContain("top: var(--app-titlebar-height)");
    expect(app).not.toContain("syncNativeWindowTheme");
    expect(workbench).not.toContain("ApplyNativeWindowTheme");
  });

  test("keeps settings and side panels inside the title-bar-adjusted viewport", async () => {
    const [css, polish] = await Promise.all([
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text(),
    ]);
    expect(css).toContain("height: calc(var(--ui-layout-height, 100vh) - var(--app-titlebar-height));");
    expect(polish).toContain("height: calc(var(--ui-layout-height, 100vh) - var(--app-titlebar-height));");
    expect(css).toContain(".settings-nav {");
    expect(css).toContain("overscroll-behavior: contain;");
    expect(css).toContain(".side-panel-host {");
    expect(css).toContain(".side-panel-pages {");
    expect(css).toContain("max-height: 100%;");
  });

  test("uses a data-backed usage dashboard instead of generic settings cards", async () => {
    const [panels, css] = await Promise.all([
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(panels).toContain('class="settings-page-body usage-dashboard"');
    expect(panels).toContain('class="usage-dashboard-summary"');
    expect(panels).toContain('class="usage-cache-disc"');
    expect(panels).toContain("usage-billing-workspace");
    expect(panels).toContain("officialTokens");
    expect(css).toContain(".usage-dashboard {");
    expect(css).toContain(".usage-dashboard-summary {");
    expect(css).toContain(".usage-cache-disc {");
  });

  test("keeps private subagent reasoning out of the user-facing panel", async () => {
    const panel = await Bun.file(new URL("../src/components/SubagentPanel.tsx", import.meta.url)).text();
    expect(panel).not.toContain("subagentReasoning");
    expect(panel).not.toContain("推理记录");
    expect(panel).toContain("工具活动");
  });

  test("shows Skill details, safe file actions, and sliding enable controls", async () => {
    const [settings, workbench, types, css] = await Promise.all([
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(settings).toContain("readSkillDetail(name)");
    expect(settings).toContain("<SkillCodeViewer");
    expect(settings).toContain('detailMode() === "document"');
    expect(settings).toContain("skillDocumentBody(current().content)");
    expect(settings).toContain("expandCodeBlocks: true");
    expect(settings).toContain("<Portal>");
    expect(settings).toContain('title="打开 SKILL.md 查看器"');
    expect(settings).toContain("updateSkillEnabled(skill.name, value)");
    expect(settings).toContain('label={`启用 ${skill.name}`}');
    expect(settings).toContain('title="本轮 Skill 注入"');
    expect(settings).toContain("triggeredSkillCharacters");
    expect(settings).toContain("triggeredSkillTokens");
    expect(workbench).toContain("ReadSkillDetail?:");
    expect(workbench).toContain("OpenSkillFile?:");
    expect(workbench).toContain("RevealSkillFile?:");
    expect(types).toContain("export type SkillDetail");
    expect(types).toContain("triggeredSkillNames: string[];");
    expect(types).toContain("skills: SkillsSettings;");
    expect(css).toContain(".skill-viewer-overlay {");
    expect(css).toContain(".skill-document {");
    expect(css).toContain(".settings-switch input:focus-visible + span");
  });

	test("provides a permissioned plugin manager for office artifacts and local extensions", async () => {
		const [app, constants, panels, services, types, css] = await Promise.all([
			Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/constants.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
			Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
			Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
		]);
		expect(constants).toContain('{ id: "plugins", label: "插件"');
		expect(panels).toContain("<PluginSettingsPanel");
		expect(panels).toContain('title="安装本地插件"');
		expect(panels).toContain('title="刷新插件目录"');
		expect(panels).toContain("updatePermission(plugin().id, permission.key, value)");
		expect(panels).toContain("void props.uninstallPlugin(plugin().id)");
		expect(services).toContain('id: "office-artifacts"');
		expect(services).toContain("内置产物引擎不依赖本机 Office");
		expect(app).toContain("setState(await installPlugin(source))");
		expect(app).toContain("setState(await refreshPlugins())");
		expect(app).toContain("setState(await uninstallPlugin(id))");
		expect(services).toContain("RefreshPlugins?:");
		expect(services).toContain("SelectPluginDirectory?:");
		expect(services).toContain("InstallPlugin?:");
		expect(services).toContain("UninstallPlugin?:");
		expect(services).toContain("RevealPlugin?:");
		expect(types).toContain("export type PluginSettings");
		expect(types).toContain("export type PluginStatus");
		expect(css).toContain(".plugin-settings-page {");
		expect(css).toContain("grid-template-columns: 250px minmax(0, 1fr);");
		expect(css).toMatch(/@media \(max-width: 980px\)[\s\S]*?\.plugin-settings-page \{[\s\S]*?grid-template-columns: 1fr;/);
		expect(css).toMatch(/@media \(max-width: 620px\)[\s\S]*?\.plugin-list \{[\s\S]*?grid-template-columns: 1fr;/);
	});

	test("provides a verified extension catalog with CodeGraph lifecycle actions", async () => {
		const [app, constants, panels, services, types, css, polish] = await Promise.all([
			Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/constants.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
			Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
			Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
			Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text(),
		]);
		expect(constants).toContain('{ id: "extensions", label: "扩展中心"');
		expect(panels).toContain("<ExtensionSettingsPanel");
		expect(panels).toContain("来源已核验");
		expect(panels).toContain("runExtensionProjectAction(item.id, action.id)");
		expect(panels).toContain('class="extension-action-output"');
		expect(services).toContain("GetExtensionCatalog?:");
		expect(services).toContain("InstallExtension?:");
		expect(services).toContain('id: "mcp.codegraph"');
		expect(types).toContain("export type ExtensionCatalogState");
		expect(types).toContain("export type ExtensionOperationResult");
		expect(app).toContain("applyWorkbenchState={setState}");
		expect(css).toContain(".extension-center-page {");
		expect(css).toContain(".extension-workspace {");
		expect(css).toMatch(/\.extension-center-page \{[\s\S]*?padding-top: 0;/);
		expect(polish).toMatch(/\.settings-page-body\.extension-center-page\s*\{[^}]*padding-top: 0;/);
		expect(css).toMatch(/\.extension-toolbar \{[\s\S]*?isolation: isolate;/);
		expect(css).toMatch(/\.extension-workspace \{[\s\S]*?grid-auto-rows: max-content;[\s\S]*?height: max-content;/);
		expect(css).toMatch(/\.extension-workspace \{[\s\S]*?grid-template-columns: minmax\(290px, \.82fr\) minmax\(0, 1\.38fr\);/);
		expect(css).not.toMatch(/\.extension-workspace\s*\{[^}]*border-bottom:/);
		expect(css).toMatch(/@media \(max-width: 980px\)[\s\S]*?\.extension-workspace \{[\s\S]*?grid-template-columns: 1fr;/);
		expect(css).toMatch(/@media \(max-width: 620px\)[\s\S]*?\.extension-list \{[\s\S]*?grid-template-columns: 1fr;/);
	});

	test("renders generated office artifacts without treating available files as edits", async () => {
		const messageContent = await Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text();
		const artifactStart = messageContent.indexOf("function activityArtifacts");
		const artifactEnd = messageContent.indexOf("function friendlyToolName", artifactStart);
		const artifactHelper = messageContent.slice(artifactStart, artifactEnd);
		expect(artifactHelper).toContain('part.fileAction !== "available"');

		const changedStart = messageContent.indexOf("function isChangedFilePart");
		const changedEnd = messageContent.indexOf("function groupRenderBlocks", changedStart);
		const changedHelper = messageContent.slice(changedStart, changedEnd);
		expect(changedHelper).toContain('part.fileAction === "created"');
		expect(changedHelper).toContain('part.fileAction === "modified"');
		expect(changedHelper).not.toContain('part.fileAction === "available"');
	});

  test("streams, deduplicates, and renders provider safety notices", async () => {
    const [app, messageContent, css, types] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
      Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
    ]);
    expect(app.match(/case "provider_notice"/g)?.length).toBe(2);
    expect(app).toContain("providerNoticeIdentity(part)");
    expect(messageContent).toContain("<ProviderNotice");
    expect(messageContent).toContain("服务端调整了本轮模型");
    expect(css).toContain(".op-provider-notice {");
    expect(css).toContain("color-mix(in srgb, var(--accent)");
    expect(types).toContain('kind: "provider_notice"');
  });

  test("applies live usage and cache state while a task is running", async () => {
    const [app, types] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
    ]);
    expect(types).toContain('"usage_state"');
    expect(types).toContain("usageState?: LiveUsageState");
    expect(app).toContain('case "usage_state"');
    expect(app).toContain("applyLiveUsageState(event)");
    expect(app).toContain("usageMetrics: usageState.usageMetrics");
    expect(app).toContain("usageLedger: usageState.usageLedger");
  });

	test("keeps structured waiting and retrying states visible during tool execution", async () => {
		const [app, types, css] = await Promise.all([
			Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
			Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
			Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
		]);
		expect(types).toContain('"running" | "waiting" | "retrying"');
		expect(app.match(/status: toolEventMessage\(event\)/g)?.length).toBe(2);
		expect(app.match(/statusKind: toolEventStatusKind\(event\.status\)/g)?.length).toBe(2);
		expect(app).toContain('status === "waiting" || status === "retrying"');
		expect(css).toContain(".op-stream-state.waiting");
		expect(css).toContain(".op-stream-state.retrying");
	});

	test("merges renamed download progress and final results by tool call identity", async () => {
		const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
		const mergeStart = app.indexOf("function mergeLiveToolResultParts");
		const mergeEnd = app.indexOf("function isTerminalToolStatus", mergeStart);
		const mergeHelper = app.slice(mergeStart, mergeEnd);
		expect(mergeHelper).toContain("currentPart.toolCallId === part.toolCallId");
		expect(mergeHelper).toContain("part.toolCallId !== currentPart.toolCallId");
		expect(mergeHelper).toContain("toolCallId: part.toolCallId || currentPart.toolCallId");
	});

  test("merges and renders dynamic subagents independently from the fixed AI team", async () => {
    const [app, messageContent, panel, host, services, css, types] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
	  Bun.file(new URL("../src/components/SubagentPanel.tsx", import.meta.url)).text(),
	  Bun.file(new URL("../src/components/SidePanelHost.tsx", import.meta.url)).text(),
	  Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
      Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
    ]);
    expect(types).toContain('kind: "subagent"');
    expect(types).toContain('agentType: "explore" | "review" | "implement"');
    expect(app).toContain('item.kind === "subagent" && item.taskId === part.taskId');
    expect(messageContent).toContain("<SubagentRun");
	expect(messageContent).toContain("export function SubagentDock");
    expect(messageContent).toContain('aria-label="动态子代理执行记录"');
    expect(messageContent).toContain('part.name === "delegate_task"');
    expect(css).toContain(".op-subagent-run {");
    expect(css).toContain(".op-subagent-item.running .op-subagent-status");
	expect(app).toContain("stopSubagent(parent.taskID, part.taskId)");
	expect(app).toContain("selectedSubagentTaskID");
	expect(host).toContain('<SubagentPanel');
	expect(panel).toContain('class="subagent-panel-scroll"');
	expect(panel).not.toContain("textarea");
	expect(services).toContain("StopSubagent?:");
	expect(types).toContain("subagentOutput?: string;");
	expect(types).toContain("activities?: Array<{");
  });

  test("keeps a one-line user message compact", async () => {
    const css = await Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text();
    expect(css).toContain(".op-user-bubble .md-body p { margin: 0; }");
    expect(css).toContain(".op-user-bubble .op-stream { gap: 0;");
  });

  test("does not draw a line inside the rich composer", async () => {
    const css = (await Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text()).replaceAll("\r\n", "\n");
    const ruleStart = css.indexOf(".composer-text-editor,\n.composer-tail-editor");
    expect(ruleStart).toBeGreaterThanOrEqual(0);
    const editorRule = css.slice(ruleStart);
    expect(editorRule).toContain("border-bottom: 0;");
    expect(editorRule).toContain("box-shadow: none;");
    expect(editorRule).toContain("text-decoration: none;");
  });

  test("keeps composer controls inside a narrow chat pane beside the right panel", async () => {
    const [app, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(app).toContain('class="composer-tools"');
    expect(app).toContain('class="composer-send-controls"');
    expect(css).toContain("container-name: chat-pane;");
    expect(css).toContain("@container chat-pane (max-width: 620px)");
    expect(css).toContain(".composer-tools {");
    expect(css).toContain("flex-basis: 100%;");
    expect(css).toContain(".composer-send-controls {");
    expect(css).toContain("flex-wrap: wrap;");
  });

	test("keeps URL chips on a separate row below one native editor", async () => {
    const [app, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
	expect(app).toContain('<textarea');
	expect(app).toContain('class="composer-link-row"');
	expect(app).not.toContain("composerTailEditorRef");
	expect(css).toContain(".composer-link-row {");
	expect(css).toContain(".composer-text-editor::placeholder");
  });

  test("keeps the link remove action inside the composer chip", async () => {
    const css = (await Bun.file(new URL("../src/styles.css", import.meta.url)).text()).replace(/\r\n?/g, "\n");
    const ruleStart = css.indexOf(".composer-link-remove {\n  position: static;");
    expect(ruleStart).toBeGreaterThanOrEqual(0);
    const rule = css.slice(ruleStart, css.indexOf("}", ruleStart) + 1);
    expect(rule).toContain("position: static;");
    expect(rule).toContain("width: 18px;");
    expect(rule).not.toContain("top: -7px;");
    expect(rule).not.toContain("right: -7px;");
  });

	test("uses one native composer while retaining toolbar undo and redo", async () => {
    const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
	expect(app).toContain("ref={composerEditorRef}");
	expect(app).toContain("event.currentTarget.value");
	expect(app).not.toContain("composerTailEditorRef");
    expect(app).toContain("undoComposerInput");
    expect(app).toContain("redoComposerInput");
    expect(app).toContain('title="撤销输入 (Ctrl+Z)"');
  });

  test("renders generated files as collapsed artifacts with HTML open actions", async () => {
    const messageContent = await Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text();
    expect(messageContent).toMatch(/<details[\s\S]{0,120}class="op-file-artifact"/);
    expect(messageContent).toContain('runFromSummary(event, "view")');
    expect(messageContent).toContain("在右侧查看文件");
    expect(messageContent).toContain("在内置浏览器中预览");
  });

  test("opens workspace files in a non-Git right-side file panel", async () => {
    const [app, messageContent, reviewPanel] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/ReviewPanel.tsx", import.meta.url)).text(),
    ]);
    expect(app).toContain("onOpenWorkspaceFile={handleOpenWorkspaceFile}");
    expect(app).toContain("fileRequest={workspaceFileRequest()}");
    expect(messageContent).toContain("<FileChangesSummary");
    expect(messageContent).toContain('onOpenFile={props.onOpenWorkspaceFile}');
    expect(messageContent).toContain("data-workspace-path");
    expect(reviewPanel).toContain("readWorkspaceFile(target)");
    expect(reviewPanel).toContain("文件会在这里打开，不需要 Git 仓库。");
    expect(reviewPanel).not.toContain("当前工作区不是 Git 仓库");
  });

  test("uses a read-only CodeMirror viewer and compact read_file links", async () => {
    const [messageContent, reviewPanel, codeViewer, inlineCode, css, polish] = await Promise.all([
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/ReviewPanel.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/CodeViewer.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/InlineCodePreview.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text(),
    ]);
    expect(reviewPanel).toContain("<CodeViewer");
    expect(reviewPanel).not.toContain('class="review-code-line"');
    expect(codeViewer).toContain("basicSetup");
    expect(codeViewer).toContain("LanguageDescription.matchFilename");
    expect(codeViewer).toContain("EditorState.readOnly.of(true)");
    expect(codeViewer).toContain("EditorView.scrollIntoView");
    expect(messageContent).toContain('class="op-read-file"');
    expect(messageContent).toContain('props.onOpenWorkspaceFile?.(reference.path, "file", reference.startLine)');
    expect(messageContent).toContain("<InlineCodePreview");
    expect(inlineCode).toContain("parseInlineCode(props.content, props.startLine)");
    expect(inlineCode).toContain("highlightCodeBlock(code(), codeLanguageForPath(props.path))");
    expect(inlineCode).toContain('class="op-inline-code-gutter"');
    expect(inlineCode).toContain('title="在右侧代码查看器中打开"');
    expect(css).toContain(".op-inline-code-scroll");
    expect(css).toContain("contain: inline-size;");
    expect(css).toContain("scrollbar-gutter: stable;");
    expect(polish).toContain("overflow-x: hidden;");
  });

  test("shows edit diffs directly in the conversation activity", async () => {
    const [messageContent, inlineDiff] = await Promise.all([
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/InlineDiffPreview.tsx", import.meta.url)).text(),
    ]);
    expect(messageContent).toContain('<Match when={part.kind === "diff"}>');
    expect(messageContent).not.toContain('part.kind === "diff" && props.item.category !== "edit"');
    expect(messageContent).toContain("<InlineDiffPreview");
    expect(inlineDiff).toContain("parseInlineDiff(props.patch)");
    expect(inlineDiff).toContain('title={copied() ? "已复制" : "复制修改"}');
  });

  test("shows a compact file-change summary at the end of a completed response", async () => {
    const [app, messageContent, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
    ]);
    expect(messageContent.indexOf("<FileChangesSummary")).toBeGreaterThan(messageContent.indexOf("<For each={blocks()}"));
    expect(messageContent).toContain("已编辑 {props.files.length} 个文件");
    expect(messageContent).toContain("再显示 ${hiddenCount()} 个文件");
    expect(messageContent).toContain('title="撤销本轮文件修改"');
    expect(messageContent).toContain('title="在右侧审阅修改"');
    expect(app).toContain("const handleUndoMessageChanges = async");
    expect(app).toContain("setState(await forkFromMessage(sourceMessage.eventId, activeProjectID(), activeSessionID()))");
    expect(app).toContain("hideFileChangesSummary={message.streaming}");
    expect(css).toContain(".op-file-changes-head");
  });

  test("collapses tool output, markdown code, read_file code, and diffs by default", async () => {
    const [messageContent, markdown, inlineCode, inlineDiff] = await Promise.all([
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/lib/markdown.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/InlineCodePreview.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/InlineDiffPreview.tsx", import.meta.url)).text(),
    ]);
    expect(messageContent).not.toContain('open={status() === "error"');
    expect(markdown).toContain('`<details class="code-block"');
    expect(inlineCode).toContain("const [expanded, setExpanded] = createSignal(false)");
    expect(inlineDiff).toContain("const [expanded, setExpanded] = createSignal(false)");
    expect(inlineDiff).toContain('innerHTML={highlightCode(row.content || " ", language())');
  });

  test("preserves the embedded browser while switching to a file tab", async () => {
    const [app, host, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/SidePanelHost.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    const openFileStart = app.indexOf("const handleOpenWorkspaceFile");
    const openFileEnd = app.indexOf("const handleOpenBrowser", openFileStart);
    const openFileHandler = app.slice(openFileStart, openFileEnd);
    expect(openFileHandler).toContain('setSidePanelView("files")');
    expect(openFileHandler).not.toContain("setBrowserPreview(undefined)");
    expect(app).toContain("<SidePanelHost");
    expect(host).toContain('type SidePanelView = "browser" | "files"');
    expect(host).toContain('suspended={props.browserSuspended || activeView() !== "browser"}');
    expect(host).toContain('onClick={() => props.onSelectView("browser")}');
    expect(host).toContain('onClick={() => props.onSelectView("files")}');
    expect(css).toContain(".side-panel-page.inactive");
    expect(css).toContain("visibility: hidden;");
  });

  test("keeps plan, AI team, and subagent status in separate centered panels above the composer", async () => {
    const [app, messageContent, reasoningMenu, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/ReasoningMenu.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(messageContent).not.toContain('<Match when={block.kind === "progress"}>');
    expect(messageContent).toContain('if (part.kind === "task_progress") {');
    expect(app).toContain('class="execution-status-dock"');
    expect(app).toContain('activeSubagentParts().length > 0 || pausedTeamTask()');
    expect(app).toContain('pausedTeamTask()].filter(Boolean).length > 1');
    expect(app).toContain('<TeamRun parts={activeTeamParts()} docked />');
	expect(app).toContain('<SubagentDock');
    expect(messageContent).toContain("hideTeamRun?: boolean;");
    expect(css).toContain(".execution-status-dock.combined");
	expect(css).toContain("flex-wrap: wrap;");
	expect(css).toContain(".execution-status-dock > .op-subagent-dock");
    expect(messageContent).toContain('<details class="op-team-run docked"');
    expect(messageContent).toContain('class="op-team-stage-track"');
    expect(reasoningMenu).toContain("<Portal>");
  });

  test("keeps overlays out of clipped layout and hides side panels behind settings", async () => {
    const [app, reasoningMenu, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/ReasoningMenu.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(reasoningMenu).toContain('triggerRef.getBoundingClientRect()');
    expect(reasoningMenu).toContain('data-placement={position().placement}');
    expect(reasoningMenu).toContain('document.addEventListener("scroll", reposition, true)');
    expect(css).toContain("position: fixed;");
    expect(app).toContain('<Show when={!drawerOpen() && (Boolean(browserPreview()) || reviewOpen() || Boolean(selectedSubagent()))}>');
    expect(app).toContain('"side-panel-open": !drawerOpen() && (Boolean(browserPreview()) || reviewOpen() || Boolean(selectedSubagent()))');
  });

  test("keeps the jump-to-bottom action centered above the plan and composer", async () => {
    const [app, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(app).toContain('<div class="chat-jump-bottom-dock">');
    expect(app.indexOf('class="chat-jump-bottom-dock"')).toBeLessThan(app.indexOf('class="execution-status-dock"'));

    const dockStart = css.indexOf(".chat-jump-bottom-dock {");
    const dockRule = css.slice(dockStart, css.indexOf("}", dockStart) + 1);
    expect(dockRule).toContain("height: 0;");
    expect(dockRule).toContain("pointer-events: none;");

    const buttonStart = css.indexOf(".chat-jump-bottom {");
    const buttonRule = css.slice(buttonStart, css.indexOf("}", buttonStart) + 1);
    expect(buttonRule).toContain("bottom: 12px;");
    expect(buttonRule).toContain("left: 50%;");
    expect(buttonRule).toContain("transform: translateX(-50%);");
    expect(buttonRule).not.toContain("right: max(");
  });

  test("uses a centered theme-aware app dialog instead of native confirmations", async () => {
    const [app, settings, dialog, polish] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/ConfirmDialog.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text(),
    ]);
    expect(`${app}\n${settings}`).not.toContain("window.confirm");
    expect(app).toContain("<ConfirmDialog request={confirmation()} onResolve={resolveConfirmation} />");
    expect(settings).toContain("confirmAction: (request: ConfirmationRequest) => Promise<boolean>");
    expect(dialog).toContain("<Portal>");
    expect(dialog).toContain('class="app-confirm-overlay"');

    const overlayStart = polish.indexOf(".app-confirm-overlay {");
    const overlayRule = polish.slice(overlayStart, polish.indexOf("}", overlayStart) + 1);
    expect(overlayRule).toContain("position: fixed;");
    expect(overlayRule).toContain("inset: 0;");
    expect(overlayRule).toContain("place-items: center;");
    expect(polish).toContain(':root[data-theme="light"] .app-confirm-overlay');
    expect(polish).toContain("background: var(--bg-card);");
  });

  test("provides real updater and persistent Agent automation settings", async () => {
    const [app, panels, services, constants, types, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/constants.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(constants).toContain('{ id: "automation", label: "自动化任务"');
    expect(constants).toContain('{ id: "about", label: "关于 EchoCode"');
    expect(panels).toContain("<AutomationSettingsPanel");
    expect(panels).toContain("runAutomationTaskNow");
    expect(panels).toContain("stopAutomationTask");
    expect(panels).toContain("saveAutomationTask");
    expect(panels).toContain("<AboutSettingsPanel");
    expect(panels).toContain("checkForUpdates");
    expect(panels).toContain("installUpdate");
    expect(services).toContain('runtime.EventsOn("automation:state"');
    expect(services).toContain('runtime.EventsOn("update:state"');
    expect(types).toContain("export type AutomationTask");
    expect(types).toContain("export type UpdateState");
    expect(css).toContain(".automation-settings-page");
    expect(css).toContain(".about-download-progress");
    expect(app).toContain("onUpdateState(presentUpdateBanner)");
    expect(app).toContain('class="app-update-banner"');
    expect(app).toContain("startUpdateBannerCountdown");
    expect(app).toContain('new Set(["available", "downloading", "downloaded", "installing"])');
    expect(css).toContain("top: calc(var(--app-titlebar-height) + 20px);");
    expect(css).toContain(".app-update-banner-timer span.indeterminate");
    expect(css).toContain("@keyframes update-banner-indeterminate");
  });

  test("removes the redundant sidebar letter mark", async () => {
    const [app, css, polish] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/polish.css", import.meta.url)).text(),
    ]);
    expect(app).not.toContain('class="brand-mark"');
    expect(css).not.toContain(".brand-mark");
    expect(polish).not.toContain(".brand-mark");
  });

  test("switches to the backend replacement after deleting the exact active conversation", async () => {
    const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
    const deleteStart = app.indexOf("const handleDeleteSession");
    const deleteEnd = app.indexOf("const beginEditMessage", deleteStart);
    const handler = app.slice(deleteStart, deleteEnd);
    expect(handler).toContain("projectID === currentProjectID && session.id === currentSessionID");
    expect(handler).not.toContain("session.isActive");
    expect(handler).toContain("reloadAfterSessionChange(nextState, false)");
    expect(handler).toContain("nextState.activeProjectId,");
    expect(handler).toContain("nextState.activeSessionId,");
    expect(handler).toContain("delete next[sessionKey]");
  });

  test("provides project-scoped conversation actions from the context menu", async () => {
    const [app, services] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
    ]);
    expect(app).toContain("onContextMenu={(event) => openSessionMenu(event, project, session)}");
    expect(app).toContain('class="project-action-menu session-action-menu"');
    expect(app).toContain("重命名");
    expect(app).toContain("打开项目目录");
    expect(app).toContain("复制工作目录");
    expect(app).toContain("复制会话 ID");
    expect(app).toContain("永久删除");
    expect(app).toContain("renameSession(dialog.project.id, dialog.session.id, title)");
    expect(services).toContain("binding.RenameProjectSession(projectID, sessionID, title)");
    expect(services).toContain("binding.ArchiveProjectSession(projectID, sessionID, archived)");
    expect(services).toContain("binding.DeleteProjectSession(projectID, sessionID)");
  });

  test("renders a workspace-focused empty view without a provider-specific link", async () => {
    const [app, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
    ]);
    expect(app).not.toContain("连接 DeepSeek");
    expect(app).toContain('class="welcome-brand"');
    expect(app).toContain('class="welcome-prompt-grid"');
    expect(app).toContain("primeWelcomePrompt");
    expect(app).toContain("梳理当前项目");
    expect(app).toContain("检查当前修改");
    expect(css).toContain(".welcome-prompt-grid");
    expect(css).toContain("grid-template-columns: repeat(2, minmax(0, 1fr));");
  });

  test("allows independent background tasks for different sessions", async () => {
    const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
    expect(app).toContain("startChatMessageForSession(projectID, sessionID, prompt, attachments)");
    expect(app).toContain("const isSessionBusy = (projectID: string, sessionID: string)");
    expect(app).toContain("if (isSessionBusy(projectID, sessionID))");
    expect(app).toContain("sessionIdentityKey(projectID, sessionID)");
    expect(app).toContain("const backgroundTaskCount = createMemo");
    expect(app).toContain("const activeTasks = await getActiveChatTasks()");
    expect(app).not.toContain("if ((!prompt && attachments.length === 0) || sendingMessage())");
  });

  test("restores conversation UI by project and session identity", async () => {
    const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
    expect(app).toContain("const [sessionViewStates, setSessionViewStates]");
    expect(app).toContain("rememberCurrentSessionView();");
    expect(app).toContain("restoreSessionView(nextState.activeProjectId, nextState.activeSessionId)");
    expect(app).toContain("reduceBackgroundTaskEvent(eventProjectID, eventSessionID, event)");
    expect(app).toContain("browserPreview: browserPreview()");
    expect(app).toContain("workspaceFileRequest: workspaceFileRequest()");
  });

  test("treats successful repository reads as usable partial results", () => {
    expect(hasUsablePartialResult([{
      kind: "tool_call",
      name: "read_repository",
      status: "ok",
      input: "https://github.com/SliyRainbow/EchoCode",
      output: "Repository: SliyRainbow/EchoCode\nCommit: abc123",
    }])).toBe(true);
    expect(hasUsablePartialResult([{
      kind: "tool_call",
      name: "read_repository",
      status: "error",
      output: "request failed",
    }])).toBe(false);
  });

  test("shows render and visual inspection as distinct live activities", async () => {
    const messageContent = await Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text();
    expect(messageContent).toContain('case "render_artifact": return "render";');
    expect(messageContent).toContain('case "inspect_visual": return "visual";');
    expect(messageContent).toContain('name === "render_artifact"');
    expect(messageContent).toContain('name === "inspect_visual"');
    expect(messageContent).toContain('part.status === "waiting"');
    expect(messageContent).toContain('part.status === "retrying"');
    expect(messageContent).toContain("渲染预览");
    expect(messageContent).toContain("视觉检查");
  });

  test("treats successful webpage reads as usable partial results", () => {
    expect(hasUsablePartialResult([{
      kind: "tool_call",
      name: "read_webpage",
      status: "ok",
      input: "https://example.com",
      output: "Title: Example\n\nPage content:\nActual page text",
    }])).toBe(true);
    expect(hasUsablePartialResult([{
      kind: "tool_call",
      name: "read_webpage",
      status: "error",
      output: "HTTP 403",
    }])).toBe(false);
  });

  test("only restores the composer when a stopped turn produced no meaningful output", async () => {
    const app = await Bun.file(new URL("../src/app.tsx", import.meta.url)).text();
    expect(app).toContain('case "cancelled": {');
    expect(app).toContain("hasMeaningfulTurnOutput(result, eventTask?.assistantMessage)");
    expect(app).toMatch(/if \(!retainedTurn\) \{\s*rollbackOptimisticTurn\(eventProjectID, eventSessionID\);/);
    expect(app).toMatch(/if \(accepted\) \{[\s\S]{0,220}Do not erase the turn here\.[\s\S]{0,80}return;/);
    expect(app).toMatch(/case "cancelled": \{[\s\S]{0,1600}cancelled: true/);
  });

  test("retains only user-visible partial output", () => {
    expect(hasMeaningfulTurnOutput({ content: "partial", turnCommitted: true })).toBe(true);
    expect(hasMeaningfulTurnOutput({ content: "", turnCommitted: false }, { content: "", reasoning: "started", parts: [] })).toBe(false);
    expect(hasMeaningfulTurnOutput({ content: "", turnCommitted: false }, {
      content: "",
      parts: [{ kind: "tool_call", name: "terminal", status: "ok", input: "Get-ChildItem" }],
    })).toBe(false);
    expect(hasMeaningfulTurnOutput({ content: "", turnCommitted: false }, {
      content: "",
      parts: [{ kind: "provider_notice", noticeKind: "policy_error", message: "blocked" }],
    })).toBe(false);
	expect(hasMeaningfulTurnOutput(undefined, {
	  content: "",
	  reasoning: "private reasoning only",
	  parts: [],
	})).toBe(false);
	expect(hasMeaningfulTurnOutput(undefined, {
	  content: "",
	  parts: [{ kind: "timeline_note", message: "正在分析任务", status: "running" }],
	})).toBe(false);
  });

  test("shows verifiable progress notes while work is running and restores them from history", async () => {
	const [app, messageContent, timeline, types, css] = await Promise.all([
	  Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
	  Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
	  Bun.file(new URL("../src/lib/timeline.ts", import.meta.url)).text(),
	  Bun.file(new URL("../src/types.ts", import.meta.url)).text(),
	  Bun.file(new URL("../src/styles.css", import.meta.url)).text(),
	]);
	expect(app).toContain("updateLiveTimelineParts(live.parts, event)");
	expect(app).toContain('case "heartbeat":');
	expect(app).toContain("displayMessageParts(message.parts, message.content, message.streaming)");
	expect(app).toContain("flushLiveAssistantMessage(message)");
	expect(timeline).toContain('if (event.type === "heartbeat" || event.type === "started") return parts ?? [];');
	expect(timeline).toContain('normalized.startsWith("上游模型仍在处理")');
	expect(timeline).toContain("appendLiveAssistantText");
	expect(timeline).toContain('kind: "timeline_note"');
	expect(messageContent).toContain("function TimelineNote");
	expect(messageContent).toContain('block.kind === "timeline"');
	expect(messageContent).toContain('part.name === "update_plan"');
	expect(types).toContain('kind: "timeline_note"');
	expect(css).toContain(".op-timeline-note {");
  });

  test("groups shell, terminal, and SSH actions into nested command rows", async () => {
    const [messageContent, css] = await Promise.all([
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
    ]);
    expect(messageContent).toMatch(/<CommandActivityList[\s\S]{0,180}parts={props\.item\.parts}/);
    expect(messageContent).toContain('class="op-command-entry"');
		expect(messageContent).toContain('case "git_repository": return "command";');
		expect(messageContent).toContain('props.part.name === "run_command" || props.part.name === "terminal" || props.part.name === "ssh" || props.part.name === "git_repository"');
		expect(messageContent).toContain('if (name === "git_repository") return "Git";');
    expect(css).toContain(".op-command-list {");
    expect(css).toContain(".op-command-entry > summary {");
    expect(css).toContain(".op-command-entry[open] > summary > svg {");
  });

  test("groups consecutive research actions under one stable disclosure", async () => {
    const [app, messageContent, css] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/components/chat/MessageContent.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/styles/chat.css", import.meta.url)).text(),
    ]);
    expect(messageContent).toContain("function buildActivityBatches");
    expect(messageContent).toContain('category === "web"');
    expect(messageContent).toContain('category === "repository"');
    expect(messageContent).toContain('class="op-activity-item op-activity-batch"');
    expect(messageContent).toContain("正在查阅 ${count} 项资料");
    expect(messageContent).toContain("activity-batch:${activityBatchIdentity(batch, index())}");
    expect(css).toContain(".op-activity-batch-body {");
    expect(app).toContain("const renderedMessages = createStableListViews(messages, (message) => message.id);");
    expect(app).toContain("each={renderedMessages()}");
  });

  test("loads Markdown as chat context and imports durable Markdown Skills", async () => {
    const [app, settings, workbench] = await Promise.all([
      Bun.file(new URL("../src/app.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/settings-panels.tsx", import.meta.url)).text(),
      Bun.file(new URL("../src/services/workbench.ts", import.meta.url)).text(),
    ]);
    expect(app).toContain('title="添加 Markdown 参考资料"');
    expect(app).toContain('accept=".md,.markdown,text/markdown,text/x-markdown"');
    expect(app).toContain("readChatMarkdown");
    expect(settings).toContain("导入长期 Markdown 规则");
    expect(settings).toContain("每轮自动应用");
    expect(workbench).toContain("ImportSkillMarkdown?:");
  });
});
