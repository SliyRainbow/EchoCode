// Explicit release acceptance against a real Wails executable. All application
// data and projects stay below the supplied output directory. No real provider
// credentials are read, written, or sent by this harness.
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const net = require('node:net');
const { spawn } = require('node:child_process');
const assert = require('node:assert/strict');
const { chromium } = require('../frontend/node_modules/playwright');

const executable = path.resolve(process.argv[2] || 'build/bin/EchoCode.exe');
const output = path.resolve(process.argv[3] || 'build/desktop-acceptance');
fs.mkdirSync(output, { recursive: true });
const root = fs.mkdtempSync(path.join(output, 'isolated-'));
const workspace = path.join(root, 'project-one');
const secondWorkspace = path.join(root, 'project-two');
for (const directory of [workspace, secondWorkspace, 'roaming/EchoCode', 'local', 'user'].map(p => path.isAbsolute(p) ? p : path.join(root, p))) fs.mkdirSync(directory, { recursive: true });
const report = { executable, root, startedAt: new Date().toISOString(), scenarios: [], pageErrors: [] };
let app, browser, page, server;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const freePort = () => new Promise(resolve => { const socket = net.createServer(); socket.listen(0, '127.0.0.1', () => { const port = socket.address().port; socket.close(() => resolve(port)); }); });
async function until(check, label, timeout = 20000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { const value = await check(); if (value) return value; await sleep(100); }
  throw new Error(`Timed out: ${label}`);
}
async function step(name, fn) {
  const started = Date.now();
  try { await fn(); report.scenarios.push({ name, status: 'passed', elapsedMs: Date.now() - started }); console.log(`PASS ${name}`); }
  catch (error) { report.scenarios.push({ name, status: 'failed', error: String(error), elapsedMs: Date.now() - started }); throw error; }
}
const call = (method, ...args) => page.evaluate(({ method, args }) => window.go.main.App[method](...args), { method, args });
async function listen() {
  await page.evaluate(() => {
    window.__acceptanceEvents = [];
    window.__acceptanceApprovals = [];
    window.runtime.EventsOn('chat:task', event => window.__acceptanceEvents.push(event));
    window.runtime.EventsOn('approval:request', event => window.__acceptanceApprovals.push(event));
  });
}
async function finishTask(id, expected = 'completed') {
  const terminal = await until(() => page.evaluate(id => window.__acceptanceEvents.find(e => e.taskId === id && ['completed', 'failed', 'cancelled'].includes(e.type)), id), `task ${id}`);
  assert.equal(terminal.type, expected, terminal.message);
  await until(async () => !(await call('GetActiveChatTasks')).some(t => t.taskId === id && ['running', 'stopping', 'queued'].includes(t.status)), 'host idle');
  return terminal;
}
async function chat(prompt) {
  return finishTask(await call('StartChatMessage', prompt));
}

async function main() {
  server = http.createServer(async (req, res) => {
    if (req.url === '/page') { res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end('<!doctype html><title>Native browser acceptance</title><h1 id="marker">BROWSER_NATIVE_OK</h1>'); return; }
    if (req.url === '/v1/models') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ data: [{ id: 'acceptance-model' }] })); return; }
    if (req.url !== '/v1/chat/completions') { res.writeHead(404); res.end(); return; }
    let raw = ''; for await (const chunk of req) raw += chunk;
    const request = JSON.parse(raw);
    const messages = request.messages || [];
    const user = [...messages].reverse().find(m => m.role === 'user');
    const prompt = typeof user?.content === 'string' ? user.content : JSON.stringify(user?.content);
    const last = messages.at(-1);
    const emit = value => res.write('data: ' + (typeof value === 'string' ? value : JSON.stringify(value)) + '\n\n');
    res.setHeader('Content-Type', 'text/event-stream');
    if (prompt.includes('NATIVE_STOP') && !prompt.includes('NATIVE_AFTER_STOP')) {
      emit({ choices: [{ delta: { content: 'Partial native fixture' } }] });
      const timer = setInterval(() => res.write(': heartbeat\n\n'), 1000);
      res.on('close', () => clearInterval(timer));
      return;
    }
    const mode = prompt.match(/NATIVE_WRITE_([ABC])/u)?.[1];
    if (mode && last?.role === 'user') {
      emit({ choices: [{ delta: { reasoning_content: 'Checking the isolated fixture.' } }] });
      emit({ choices: [{ delta: { tool_calls: [{ index: 0, id: 'call-' + mode + '-' + Date.now(), type: 'function', function: { name: 'write_file', arguments: JSON.stringify({ path: 'native.txt', content: 'native-' + mode }) } }] }, finish_reason: 'tool_calls' }] });
    } else {
      emit({ choices: [{ delta: { content: prompt.includes('NATIVE_CONTEXT') ? 'Fixture context result. '.repeat(300) : mode ? 'NATIVE_WRITE_OK_' + mode : 'NATIVE_OK' }, finish_reason: 'stop' }] });
    }
    emit({ choices: [], usage: { prompt_tokens: 30, completion_tokens: 5, total_tokens: 35 } });
    emit('[DONE]'); res.end();
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const baseURL = `http://127.0.0.1:${server.address().port}`;
  const settings = {
    schemaVersion: 13, workspaceRoot: workspace, filesystemAccess: 'workspace-write', sandboxMode: 'workspace-write',
    approvalPolicy: 'never', networkAccess: false, shellAccess: false, allowDestructiveOps: false,
    browser: { enabled: false }, mcp: { servers: [] }, team: { enabled: false }, update: { autoCheck: false, autoDownload: false },
    model: { selectedProviderId: 'acceptance-local', selectedModelId: 'acceptance-model', providers: [{ id: 'acceptance-local', name: 'Native acceptance fixture', protocol: 'local', apiType: 'chat-completions', baseUrl: baseURL + '/v1', enabled: true, defaultModelId: 'acceptance-model', models: [{ id: 'acceptance-model', provider: 'acceptance-local', maxOutputTokens: 2048 }] }] },
  };
  fs.writeFileSync(path.join(root, 'roaming/EchoCode/runtime-settings.json'), JSON.stringify(settings));
  const port = await freePort();
  const appLog = fs.openSync(path.join(root, 'desktop.log'), 'a');
  async function launch() {
    app = spawn(executable, [], { cwd: root, windowsHide: true, stdio: ['ignore', appLog, appLog], env: { ...process.env, APPDATA: path.join(root, 'roaming'), LOCALAPPDATA: path.join(root, 'local'), ECHOCODE_ACCEPTANCE_ROOT: root, ECHOCODE_ACCEPTANCE_CDP_PORT: String(port) } });
    app.on('error', error => { report.processError = String(error); });
    console.log(`Acceptance process=${app.pid}, debug port=${port}, root=${root}`);
    await until(async () => { try { return (await fetch(`http://127.0.0.1:${port}/json/version`)).ok; } catch { return false; } }, 'WebView2 debug endpoint', 120000);
    browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
    page = await until(() => browser.contexts()[0]?.pages().find(p => p.url().includes('wails')), 'Wails page');
    page.on('pageerror', error => report.pageErrors.push(String(error)));
    await page.waitForFunction(() => window.go?.main?.App && window.runtime?.EventsOn);
    await listen();
  }
  async function quit() {
    if (!app || app.exitCode !== null) return;
    await page.evaluate(() => window.runtime.Quit()).catch(() => {});
    await until(() => app.exitCode !== null, 'clean app shutdown', 10000);
    browser = null;
  }
  await launch();
  let projectId, sessionId, firstCheckpoint, secondCheckpoint;
  await step('real Wails startup and isolated settings', async () => {
    const state = await call('GetWorkbenchState');
    assert.equal(state.storageError || '', '');
    assert.ok(state.configFiles.runtimeSettingsPath.startsWith(root));
    assert.equal(state.runtimeSettings.model.selectedProviderId, 'acceptance-local');
  });
  await step('project/session creation and persistence', async () => {
    const created = await call('CreateProject', 'Native acceptance', workspace);
    projectId = created.activeProjectId; sessionId = created.activeSessionId;
    await call('RenameProjectSession', projectId, sessionId, 'Desktop integration');
    await call('SetProjectPinned', projectId, true);
    const second = await call('CreateProject', 'Isolated second project', secondWorkspace);
    assert.notEqual(second.activeProjectId, projectId);
    const restored = await call('SwitchProjectSession', projectId, sessionId);
    assert.equal(path.resolve(restored.runtimeSettings.workspaceRoot), workspace);
    assert.ok((await call('GetProjectTree')).some(p => p.id === projectId && p.pinned));
  });
  await step('streamed tool execution and visible reasoning', async () => {
    const result = await chat('NATIVE_WRITE_A');
    assert.equal(result.result.content, 'NATIVE_WRITE_OK_A');
    assert.equal(result.result.reasoning, 'Checking the isolated fixture.');
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-A');
    firstCheckpoint = (await call('ListCheckpoints')).find(c => c.isCurrent).id;
    const history = await call('GetSessionMessages');
    assert.ok(history.some(m => m.role === 'assistant' && m.content === 'NATIVE_WRITE_OK_A' && m.reasoning));
  });
  await step('file rewind and branch restoration', async () => {
    await chat('NATIVE_WRITE_B');
    secondCheckpoint = (await call('ListCheckpoints')).find(c => c.isCurrent).id;
    await call('RewindToCheckpoint', firstCheckpoint);
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-A');
    await chat('NATIVE_WRITE_C');
    const branches = await call('ListBranches');
    assert.ok(branches.length >= 2);
    await call('SwitchBranch', secondCheckpoint);
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-B');
  });
  await step('file approval reaches native host and gates mutation', async () => {
    const state = await call('GetWorkbenchState');
    state.runtimeSettings.approvalPolicy = 'always';
    await call('SaveRuntimeSettings', state.runtimeSettings);
    const id = await call('StartChatMessage', 'NATIVE_WRITE_C');
    const approval = await until(() => page.evaluate(() => window.__acceptanceApprovals.at(-1)), 'file approval');
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-B');
    await call('RespondApproval', approval.id, approval.tool, true, 'once');
    await finishTask(id);
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-C');
    state.runtimeSettings.approvalPolicy = 'never';
    await call('SaveRuntimeSettings', state.runtimeSettings);
  });
  await step('stop preserves partial output and allows next message', async () => {
    const id = await call('StartChatMessage', 'NATIVE_STOP');
    await until(() => page.evaluate(id => window.__acceptanceEvents.some(e => e.taskId === id && e.type === 'delta'), id), 'partial stream');
    assert.equal(await call('StopChatMessage', id), true);
    await finishTask(id, 'cancelled');
    assert.ok((await call('GetSessionMessages')).some(m => m.content.includes('Partial native fixture')));
    await chat('NATIVE_AFTER_STOP');
  });
  await step('automation runs through real task host', async () => {
    const state = await call('SaveAutomationTask', { name: 'Native fixture automation', enabled: false, prompt: 'NATIVE_AUTOMATION', projectId, sessionId, providerId: 'acceptance-local', modelId: 'acceptance-model', schedule: { kind: 'interval', intervalMinutes: 60 } });
    const task = state.tasks.find(t => t.name === 'Native fixture automation');
    await call('RunAutomationTaskNow', task.id);
    await until(async () => { const done = (await call('GetAutomationState')).tasks.find(t => t.id === task.id); return done?.lastRun?.status === 'completed'; }, 'automation completion');
    await call('DeleteAutomationTask', task.id);
  });
  await step('native browser and workspace preview', async () => {
    // Exercise the interactive embedded surface against this fixture window.
    await page.evaluate(() => window.runtime.WindowShow());
    const state = await call('GetWorkbenchState');
    state.runtimeSettings.browser.enabled = true;
    await call('SaveRuntimeSettings', state.runtimeSettings);
    const opened = await call('OpenBrowserURL', baseURL + '/page');
    assert.ok(opened.activeTabId);
    await call('BrowserShowNativeSurface', opened.activeTabId, 180, 80, 800, 600, 1280, 820);
    await until(async () => {
      await call('GetBrowserFrame', opened.activeTabId, false);
      const current = await call('GetBrowserState');
      report.lastBrowserState = current;
      return current.tabs.some(t => t.title === 'Native browser acceptance');
    }, 'native browser navigation');
    await call('BrowserCloseTab', opened.activeTabId);
    fs.writeFileSync(path.join(workspace, 'preview.html'), '<!doctype html><h1>PREVIEW_OK</h1>');
    const preview = await call('PreviewWorkspaceFile', 'preview.html');
    assert.ok(JSON.stringify(preview).includes('http'));
  });
  await step('app restart restores project history, branches and usage', async () => {
    const before = await call('GetSessionMessages');
    const usage = (await call('GetWorkbenchState')).usageLedger;
    assert.ok(usage.totalSamples > 0);
    await quit(); await launch();
    const state = await call('GetWorkbenchState');
    assert.equal(state.activeProjectId, projectId);
    assert.equal(state.activeSessionId, sessionId);
    assert.equal((await call('GetSessionMessages')).length, before.length);
    assert.equal(state.usageLedger.totalSamples, usage.totalSamples);
    assert.ok((await call('ListBranches')).length >= 2);
  });
  await step('file tree selection and code preview work through the real desktop UI', async () => {
    await page.getByRole('button', { name: '文件与更改', exact: true }).click();
    const file = page.getByRole('treeitem', { name: 'native.txt', exact: true });
    await file.click();
    await until(async () => (await file.getAttribute('aria-selected')) === 'true', 'selected file');
    await page.locator('.cm-content').filter({ hasText: 'native-C' }).waitFor();
    assert.equal(await file.evaluate(element => getComputedStyle(element).boxShadow), 'none');
  });
  await step('permission presets save real policy and request approval allows workspace writes', async () => {
    await page.getByRole('button', { name: '任务模式与权限', exact: true }).click();
    await page.getByRole('menuitemradio', { name: '请求批准', exact: true }).click();
    await until(async () => (await call('GetWorkbenchState')).runtimeSettings.approvalPolicy === 'on-request', 'request approval preset');
    await page.keyboard.press('Escape');
    const current = await call('GetWorkbenchState');
    assert.equal(current.runtimeSettings.sandboxMode, 'workspace-write');
    assert.equal(current.runtimeSettings.filesystemAccess, 'workspace-write');
    const approvals = await page.evaluate(() => window.__acceptanceApprovals.length);
    await chat('NATIVE_WRITE_A');
    assert.equal(await page.evaluate(() => window.__acceptanceApprovals.length), approvals);
    assert.equal(fs.readFileSync(path.join(workspace, 'native.txt'), 'utf8'), 'native-A');
    await page.getByRole('button', { name: '缓存命中率', exact: true }).click();
    await page.getByRole('dialog', { name: '缓存命中率', exact: true }).waitFor();
    await page.screenshot({ path: path.join(output, 'native-cache.png'), animations: 'disabled' });
    await page.keyboard.press('Escape');
  });
  await step('manual context compression preserves history and survives desktop restart', async () => {
    for (let i = 0; i < 4; i++) await chat('NATIVE_CONTEXT ' + i + ' isolated context data. '.repeat(150));
    const before = await call('GetSessionMessages');
    const result = await call('CompressSessionContext', projectId, sessionId);
    assert.equal(result.compressed, true);
    assert.ok(result.afterTokens < result.beforeTokens);
    assert.equal((await call('GetSessionMessages')).length, before.length);
    assert.ok(result.state.deepSeekSession.compressionTriggerTokens > 0);
    await quit(); await launch();
    const state = await call('GetWorkbenchState');
    assert.ok(state.deepSeekSession.compressionCount >= 1);
    assert.ok(state.deepSeekSession.estimatedInputTokens > 0);
    assert.equal((await call('GetSessionMessages')).length, before.length);
    await page.getByRole('button', { name: '上下文用量', exact: true }).click();
    await page.getByRole('dialog', { name: '上下文用量', exact: true }).waitFor();
    await page.screenshot({ path: path.join(output, 'native-context.png'), animations: 'disabled' });
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: '设置', exact: true }).click();
    assert.notEqual(await page.locator('.app-titlebar-name').textContent(), 'EchoCode');
    assert.equal(await page.locator('.settings-screen > .drawer-head').count(), 0);
  });
  await step('settings sidebar collapse follows the workspace and About uses the shared brand', async () => {
    await page.getByRole('navigation', { name: '设置分类', exact: true }).getByRole('button', { name: '关于 EchoCode', exact: true }).click();
    await page.locator('.about-product-mark svg').waitFor();
    assert.equal(await page.locator('.about-product-mark svg').innerHTML(), await page.locator('.rail-brand svg').innerHTML());
    await page.locator('.rail-brand-divider').waitFor();
    await page.getByRole('button', { name: '收起侧栏', exact: true }).click();
    await until(async () => !(await page.getByRole('navigation', { name: '设置分类', exact: true }).isVisible()), 'settings sidebar hidden');
    await page.getByRole('navigation', { name: '工作台导航' }).getByRole('button', { name: '工作区', exact: true }).click();
    await until(async () => !(await page.locator('.mh-sidebar').isVisible()), 'workspace sidebar hidden after transition');
    await page.getByRole('button', { name: '展开侧栏', exact: true }).click();
    await until(() => page.locator('.mh-sidebar').isVisible(), 'workspace sidebar restored');
    await page.getByRole('button', { name: '设置', exact: true }).click();
    await until(() => page.getByRole('navigation', { name: '设置分类', exact: true }).isVisible(), 'settings sidebar restored');
  });
  await step('settings divider drag updates the workspace and replies use available width', async () => {
    const divider = page.getByRole('separator', { name: '调整侧栏宽度', exact: true });
    const before = Number(await divider.getAttribute('aria-valuenow'));
    const box = await divider.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + 160);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 50, box.y + 160, { steps: 8 });
    await page.mouse.up();
    const after = Number(await divider.getAttribute('aria-valuenow'));
    assert.ok(after > before + 30);
    await page.getByRole('navigation', { name: '工作台导航' }).getByRole('button', { name: '工作区', exact: true }).click();
    assert.equal(Number(await divider.getAttribute('aria-valuenow')), after);
    await divider.focus(); await divider.press('Enter');
    const pane = await page.locator('.chat-pane').boundingBox();
    const composer = await page.locator('.composer-box').boundingBox();
    assert.ok(composer.width > pane.width * 0.84);
    const footer = page.locator('.op-message-footer').last();
    const runtime = await footer.locator('.op-message-runtime').boundingBox();
    const copy = await footer.getByRole('button', { name: '复制回复', exact: true }).boundingBox();
    assert.ok(copy.x - runtime.x - runtime.width >= 12);
    await page.screenshot({ path: path.join(output, 'native-fluid-conversation.png'), animations: 'disabled' });
  });
  await page.screenshot({ path: path.join(output, 'native-desktop.png'), animations: 'disabled' });
  await quit();
  assert.deepEqual(report.pageErrors, []);
  fs.closeSync(appLog);
}

main().catch(async error => {
  report.error = String(error.stack || error);
  console.error(report.error);
  if (page && !page.isClosed()) await page.screenshot({ path: path.join(output, 'native-failure.png') }).catch(() => {});
  process.exitCode = 1;
}).finally(async () => {
  if (app && app.exitCode === null) app.kill();
  if (browser) await browser.close().catch(() => {});
  server?.closeAllConnections(); server?.close();
  report.finishedAt = new Date().toISOString();
  fs.writeFileSync(path.join(output, 'native-report.json'), JSON.stringify(report, null, 2));
});
