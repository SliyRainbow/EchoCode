import { Tabs } from "@kobalte/core/tabs";
import { AlertTriangle, Blocks, Check, CheckCircle2, ChevronDown, Download, ExternalLink, FolderOpen, GitBranch, LockKeyhole, Play, Plug, RefreshCw, Search, ShieldCheck, Wrench, X } from "lucide-solid";
import { For, Show, createEffect, createMemo, createSignal, onMount } from "solid-js";
import type { ExtensionActionResult, ExtensionCatalogPackage, ExtensionCatalogState, WorkbenchState } from "../types";
import type { ConfirmationRequest } from "./ConfirmDialog";
import { SettingsButton, SettingsIconButton } from "./ui/SettingsControls";
import { getExtensionCatalog, installExtension, refreshExtensionCatalog, revealExtension, runExtensionProjectAction, uninstallExtension, openURLInSystemBrowser } from "../services/workbench";

type ExtensionCatalogTab = "featured" | "installed" | "mcp" | "plugin" | "skill";

const extensionCatalogTabs: Array<{id: ExtensionCatalogTab; label: string}> = [
	{id: "featured", label: "推荐"},
	{id: "installed", label: "已安装"},
	{id: "mcp", label: "MCP"},
	{id: "plugin", label: "插件"},
	{id: "skill", label: "Skills"},
];

function extensionTypeLabel(type: string) {
	switch (type) {
		case "mcp": return "MCP";
		case "plugin": return "插件";
		case "skill": return "Skill";
		default: return type || "扩展";
	}
}

function extensionTypeIcon(type: string) {
	switch (type) {
		case "mcp": return <Plug size={16} />;
		case "plugin": return <Blocks size={16} />;
		case "skill": return <Wrench size={16} />;
		default: return <Download size={16} />;
	}
}

function extensionPermissionLabel(id: string) {
	const labels: Record<string, string> = {
		"process.spawn": "启动本地进程",
		"workspace.read": "读取已授权项目",
		"workspace.write": "修改已授权项目",
		"workspace.metadata.write": "写入项目索引元数据",
		"network": "访问网络",
	};
	return labels[id] ?? id;
}

function extensionCatalogSourceLabel(source?: string) {
	switch (source) {
		case "network": return "在线目录";
		case "cache": return "本地缓存";
		case "preview": return "界面预览";
		default: return "等待加载";
	}
}

function extensionCheckedAt(value?: string) {
	if (!value) return "尚未检查";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return "尚未检查";
	return date.toLocaleString("zh-CN", {month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit"});
}

export function ExtensionSettingsPanel(props: {
	applyWorkbenchState: (state: WorkbenchState) => void;
	confirmAction: (request: ConfirmationRequest) => Promise<boolean>;
}) {
	const [catalog, setCatalog] = createSignal<ExtensionCatalogState>();
	const [activeTab, setActiveTab] = createSignal<ExtensionCatalogTab>("featured");
	const [query, setQuery] = createSignal("");
	const [selectedID, setSelectedID] = createSignal("");
	const [loading, setLoading] = createSignal(false);
	const [busy, setBusy] = createSignal("");
	const [error, setError] = createSignal("");
	const [lastAction, setLastAction] = createSignal<{
		packageName: string;
		label: string;
		result: ExtensionActionResult;
	}>();

	const packages = createMemo(() => catalog()?.packages ?? []);
	const filteredPackages = createMemo(() => {
		const tab = activeTab();
		const search = query().trim().toLocaleLowerCase();
		return packages().filter((item) => {
			const tabMatch = tab === "featured"
				? item.featured
				: tab === "installed"
					? Boolean(item.installed)
					: item.type === tab;
			if (!tabMatch) return false;
			if (!search) return true;
			return [item.name, item.summary, item.publisher, item.manifest.description, ...item.manifest.capabilities]
				.join(" ")
				.toLocaleLowerCase()
				.includes(search);
		});
	});
	const selectedPackage = createMemo(() => packages().find((item) => item.id === selectedID()));
	const tabCount = (tab: ExtensionCatalogTab) => packages().filter((item) => tab === "featured"
		? item.featured
		: tab === "installed"
			? Boolean(item.installed)
			: item.type === tab).length;

	createEffect(() => {
		const visible = filteredPackages();
		if (!visible.some((item) => item.id === selectedID())) {
			setSelectedID(visible[0]?.id ?? "");
		}
	});

	const loadCatalog = async (refresh: boolean) => {
		setLoading(true);
		setError("");
		try {
			setCatalog(refresh ? await refreshExtensionCatalog() : await getExtensionCatalog());
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setLoading(false);
		}
	};

	onMount(() => void loadCatalog(false));

	const install = async (item: ExtensionCatalogPackage) => {
		const updating = Boolean(item.installed && item.updateAvailable);
		const permissionDetail = item.manifest.permissions.length > 0
			? item.manifest.permissions.map((permission) => `${permission.required ? "必需" : "可选"}：${extensionPermissionLabel(permission.id)} - ${permission.reason}`).join("\n")
			: "此扩展未声明额外权限。";
		const confirmed = await props.confirmAction({
			title: updating ? `更新 ${item.name}？` : `安装 ${item.name}？`,
			message: `${item.publisher} · ${item.manifest.version} · ${extensionTypeLabel(item.type)}`,
			detail: permissionDetail,
			confirmLabel: updating ? "更新" : "安装",
			tone: "default",
		});
		if (!confirmed) return;
		setBusy(`${item.id}:install`);
		setError("");
		try {
			const result = await installExtension(item.id);
			setCatalog(result.catalog);
			props.applyWorkbenchState(result.state);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setBusy("");
		}
	};

	const uninstall = async (item: ExtensionCatalogPackage) => {
		const confirmed = await props.confirmAction({
			title: `卸载 ${item.name}？`,
			message: "扩展程序及其 EchoCode 配置将被移除。",
			detail: "项目内由扩展生成的数据默认保留。例如 CodeGraph 的 .codegraph 索引不会被删除。",
			confirmLabel: "卸载",
			tone: "danger",
		});
		if (!confirmed) return;
		setBusy(`${item.id}:uninstall`);
		setError("");
		try {
			const result = await uninstallExtension(item.id);
			setCatalog(result.catalog);
			props.applyWorkbenchState(result.state);
			setLastAction(undefined);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setBusy("");
		}
	};

	const runProjectAction = async (item: ExtensionCatalogPackage, action: NonNullable<ExtensionCatalogPackage["manifest"]["projectActions"]>[number]) => {
		if (action.requiresConfirmation) {
			const confirmed = await props.confirmAction({
				title: `${action.label}？`,
				message: `将在当前项目中运行 ${item.name}。`,
				detail: action.writes?.length ? `可能写入：${action.writes.join("、")}` : "该操作不会修改项目文件。",
				confirmLabel: action.label,
				tone: "default",
			});
			if (!confirmed) return;
		}
		setBusy(`${item.id}:action:${action.id}`);
		setError("");
		try {
			const result = await runExtensionProjectAction(item.id, action.id);
			setLastAction({packageName: item.name, label: action.label, result});
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setBusy("");
		}
	};

  return (
    <Tabs class="settings-page-body extension-center-page" value={activeTab()} onChange={(value) => setActiveTab(value as ExtensionCatalogTab)}>
      <div class="catalog-toolbar">
        <Tabs.List class="catalog-tabs" aria-label="扩展分类">
          <For each={extensionCatalogTabs}>{(tab) => (
            <Tabs.Trigger value={tab.id} class="catalog-tab">
              <span>{tab.label}</span><small>{tabCount(tab.id)}</small>
            </Tabs.Trigger>
          )}</For>
        </Tabs.List>
        <div class="catalog-search-tools">
          <label class="catalog-search">
            <Search size={15} aria-hidden="true" />
            <input aria-label="搜索扩展" value={query()} placeholder="搜索扩展、能力或开发者" onInput={(event) => setQuery(event.currentTarget.value)} />
            <Show when={query()}><button type="button" aria-label="清除扩展搜索" onClick={() => setQuery("")}><X size={14} /></button></Show>
          </label>
          <SettingsIconButton title="刷新扩展目录" disabled={loading()} onClick={() => void loadCatalog(true)}>
            <RefreshCw size={15} classList={{ spinning: loading() }} />
          </SettingsIconButton>
        </div>
      </div>

      <Show when={catalog()?.warning}><p class="catalog-notice"><AlertTriangle size={15} />{catalog()?.warning}</p></Show>
      <Show when={error()}><p class="catalog-notice error" role="alert"><AlertTriangle size={15} />{error()}</p></Show>

      <Tabs.Content value={activeTab()} class="catalog-results">
        <div class="catalog-workspace">
          <section class="catalog-sidebar" aria-label="扩展列表" aria-busy={loading()}>
            <div class="catalog-list-heading"><h2>扩展列表</h2><span>{filteredPackages().length} 项</span></div>
            <div class="catalog-list">
              <For each={filteredPackages()} fallback={
                <div class="catalog-empty"><Search size={20} /><strong>{loading() ? "正在加载目录…" : "没有符合条件的扩展"}</strong><span>{query() ? "尝试其他关键词，或清除搜索。" : "可以切换分类查看其他扩展。"}</span></div>
              }>
                {(item) => (
                  <button class="catalog-entry" type="button" aria-label={item.name} aria-pressed={selectedID() === item.id} onClick={() => setSelectedID(item.id)}>
                    <span class="catalog-entry-icon">{extensionTypeIcon(item.type)}</span>
                    <span class="catalog-entry-copy">
                      <span class="catalog-entry-title"><strong>{item.name}</strong><small>{item.manifest.version}</small></span>
                      <span class="catalog-entry-publisher">{item.publisher} · {extensionTypeLabel(item.type)}</span>
                      <span class="catalog-entry-summary" title={item.summary}>{item.summary}</span>
                      <Show when={item.installed}><span class="catalog-entry-status"><Check size={11} />{item.updateAvailable ? "有可用更新" : "已安装"}</span></Show>
                    </span>
                  </button>
                )}
              </For>
            </div>
            <p class="catalog-sync-status"><span>{extensionCatalogSourceLabel(catalog()?.source)}</span><time dateTime={catalog()?.checkedAt}>{extensionCheckedAt(catalog()?.checkedAt)}</time></p>
          </section>

          <section class="catalog-details" aria-label="扩展详情">
            <Show when={selectedPackage()} fallback={<div class="catalog-detail-empty"><Blocks size={28} /><h2>查看扩展</h2><p>选择一个扩展，了解它的能力和所需权限。</p></div>}>
              {(item) => (
                <article class="catalog-package">
                  <header class="catalog-package-heading">
                    <span class="catalog-package-icon">{extensionTypeIcon(item().type)}</span>
                    <div class="catalog-package-title">
                      <div><h2>{item().name}</h2><span class="catalog-package-type">{extensionTypeLabel(item().type)}</span></div>
                      <p>{item().publisher}<span>v{item().manifest.version}</span><span>{item().manifest.license.spdx}</span></p>
                    </div>
                    <div class="catalog-package-actions">
                      <Show when={!item().installed}>
                        <SettingsButton variant="primary" icon={<Download size={14} />} disabled={!item().platformAvailable || Boolean(busy())} loading={busy() === `${item().id}:install`} onClick={() => void install(item())}>{item().platformAvailable ? "安装" : "当前平台不可用"}</SettingsButton>
                      </Show>
                      <Show when={item().installed && item().updateAvailable}>
                        <SettingsButton variant="primary" icon={<RefreshCw size={14} />} disabled={Boolean(busy())} loading={busy() === `${item().id}:install`} onClick={() => void install(item())}>更新</SettingsButton>
                      </Show>
                      <Show when={item().installed}>
                        <SettingsButton icon={<FolderOpen size={14} />} disabled={Boolean(busy())} onClick={() => void revealExtension(item().id).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)))}>打开目录</SettingsButton>
                        <SettingsButton variant="danger" disabled={Boolean(busy())} loading={busy() === `${item().id}:uninstall`} onClick={() => void uninstall(item())}>卸载</SettingsButton>
                      </Show>
                    </div>
                  </header>
                  <p class="catalog-package-description">{item().manifest.description}</p>
                  <Show when={item().sourceVerified}><div class="catalog-verification"><ShieldCheck size={13} />来源已核验</div></Show>

                  <div class="catalog-detail-columns">
                    <div class="catalog-main-sections">
                      <section class="catalog-section">
                        <div class="catalog-section-heading"><h3>能力</h3><span>{item().manifest.capabilities.length} 项</span></div>
                        <ul class="catalog-capabilities">
                          <For each={item().manifest.capabilities} fallback={<li>清单未声明能力</li>}>{(capability) => <li><Check size={14} />{capability}</li>}</For>
                        </ul>
                      </section>
                      <section class="catalog-section">
                        <div class="catalog-section-heading"><h3>所需权限</h3><span>安装前确认</span></div>
                        <div class="catalog-permissions">
                          <For each={item().manifest.permissions} fallback={<p class="catalog-muted">不申请额外权限</p>}>{(permission) => (
                            <div class="catalog-permission">
                              <span class="catalog-permission-icon">{permission.required ? <LockKeyhole size={16} /> : <ShieldCheck size={16} />}</span>
                              <div><strong>{extensionPermissionLabel(permission.id)}</strong><p>{permission.reason}</p></div>
                              <span class="catalog-permission-kind">{permission.required ? "必需" : "按操作授权"}</span>
                            </div>
                          )}</For>
                        </div>
                      </section>
                      <Show when={item().installed && (item().manifest.projectActions?.length ?? 0) > 0}>
                        <section class="catalog-section">
                          <div class="catalog-section-heading"><h3>当前项目</h3><span>手动执行</span></div>
                          <div class="catalog-project-actions">
                            <For each={item().manifest.projectActions}>{(action) => (
                              <SettingsButton disabled={Boolean(busy())} loading={busy() === `${item().id}:action:${action.id}`} icon={action.id === "index-status" ? <Search size={14} /> : action.id === "sync-index" ? <RefreshCw size={14} /> : <Play size={14} />} onClick={() => void runProjectAction(item(), action)}>{action.label}</SettingsButton>
                            )}</For>
                          </div>
                        </section>
                      </Show>
                      <Show when={lastAction()?.packageName === item().name ? lastAction() : undefined}>
                        {(action) => (
                          <details class="catalog-action-output">
                            <summary><CheckCircle2 size={14} /><span>{action().label}完成</span><small>退出码 {action().result.exitCode} · {action().result.durationMs} ms</small><ChevronDown size={14} /></summary>
                            <pre>{action().result.output || "操作已完成，没有输出。"}</pre>
                          </details>
                        )}
                      </Show>
                    </div>

                    <aside class="catalog-information" aria-label="版本与来源">
                      <section class="catalog-section">
                        <div class="catalog-section-heading"><h3>详细信息</h3></div>
                        <dl class="catalog-facts">
                          <div><dt>安装状态</dt><dd>{item().installed ? "已安装" : "未安装"}</dd></div>
                          <Show when={item().installed}><div><dt>本机版本</dt><dd>{item().installed?.version}</dd></div><div><dt>运行平台</dt><dd>{item().installed?.platform} / {item().installed?.arch}</dd></div></Show>
                          <div><dt>发布通道</dt><dd>{item().manifest.channel === "stable" ? "稳定版" : item().manifest.channel}</dd></div>
                          <div><dt>更新校验</dt><dd>SHA-256</dd></div>
                          <div><dt>数据位置</dt><dd>{item().installed ? "本机 LocalAppData" : "安装后创建"}</dd></div>
                        </dl>
                      </section>
                      <section class="catalog-section">
                        <div class="catalog-section-heading"><h3>来源</h3><span>{item().manifest.source.thirdParty ? "第三方开源" : "EchoCode 官方"}</span></div>
                        <div class="catalog-source-links">
                          <button type="button" onClick={() => void openURLInSystemBrowser(item().manifest.source.repository)}><GitBranch size={15} /><span><strong>源码仓库</strong><small>{item().manifest.source.repository}</small></span><ExternalLink size={13} /></button>
                          <button type="button" onClick={() => void openURLInSystemBrowser(item().manifest.source.release)}><Download size={15} /><span><strong>版本发布页</strong><small>{item().manifest.source.release}</small></span><ExternalLink size={13} /></button>
                        </div>
                      </section>
                    </aside>
                  </div>
                </article>
              )}
            </Show>
          </section>
        </div>
      </Tabs.Content>
    </Tabs>
  );
}
