import MarkdownIt from "markdown-it";
import { escapeCodeHTML, highlightCodeBlock } from "./code-highlighting";
import { parseWorkspacePathCandidate } from "./workspace-path";

// 单例 markdown 渲染器：代码块走 highlight.js 语法高亮，其余走标准 CommonMark。
// 输出的 HTML 由调用方以 innerHTML 注入；输入始终来自模型文本，markdown-it 默认转义 HTML，
// 未开启 html:true，因此不会执行内联 HTML/脚本。
const md: MarkdownIt = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: false,
});

export type MarkdownRenderOptions = {
  expandCodeBlocks?: boolean;
};

md.renderer.rules.fence = (tokens, idx, _options, env) => {
  const token = tokens[idx];
  const languageHint = token.info.trim().split(/\s+/)[0] ?? "";
  return renderCodeBlock(token.content, languageHint, Boolean((env as MarkdownRenderOptions | undefined)?.expandCodeBlocks));
};

md.renderer.rules.code_block = (tokens, idx, _options, env) =>
  renderCodeBlock(tokens[idx].content, "", Boolean((env as MarkdownRenderOptions | undefined)?.expandCodeBlocks));

md.renderer.rules.code_inline = (tokens, idx) => {
  const content = tokens[idx].content;
  const candidate = parseWorkspacePathCandidate(content);
  if (!candidate) return `<code>${escapeHtml(content)}</code>`;
  return (
    `<a class="workspace-file-link" href="#" data-workspace-path="${escapeAttr(candidate.path)}"` +
    `${candidate.line ? ` data-workspace-line="${candidate.line}"` : ""} title="在右侧查看文件">` +
    `<code>${escapeHtml(content)}</code></a>`
  );
};

// 链接统一在新窗口打开并加安全 rel。
const defaultLinkOpen =
  md.renderer.rules.link_open ??
  ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const token = tokens[idx];
  token.attrSet("target", "_blank");
  token.attrSet("rel", "noreferrer noopener");
  return defaultLinkOpen(tokens, idx, options, env, self);
};

export function renderMarkdown(source: string, options: MarkdownRenderOptions = {}): string {
  return md.render(source ?? "", options);
}

// 复制按钮的事件委托：挂在消息容器上，点到 data-role="copy" 就读同级 figure 的原始代码。
export function handleCodeCopyClick(event: MouseEvent): void {
  const target = event.target as HTMLElement | null;
  const button = target?.closest<HTMLElement>('[data-role="copy"]');
  if (!button) {
    return;
  }
  event.preventDefault();
  event.stopPropagation();
  const figure = button.closest<HTMLElement>(".code-block");
  const payload = figure?.getAttribute("data-code");
  if (!payload) {
    return;
  }
  const text = decodeCodePayload(payload);
  void navigator.clipboard?.writeText(text).then(
    () => flashCopyLabel(button, "已复制"),
    () => flashCopyLabel(button, "复制失败"),
  );
}

function renderCodeBlock(code: string, languageHint: string, expanded: boolean): string {
  const highlighted = highlightCodeBlock(code, languageHint);
  const label = highlighted.language || "text";
  const raw = encodeCodePayload(code);
  return (
    `<details class="code-block"${expanded ? " open" : ""} data-lang="${escapeAttr(label)}" data-code="${raw}">` +
    `<summary><span class="code-lang">${escapeHtml(label)}</span>` +
    `<button type="button" class="code-copy" data-role="copy">复制</button></summary>` +
    `<pre class="hljs"><code>${highlighted.html}</code></pre>` +
    `</details>\n`
  );
}

function flashCopyLabel(button: HTMLElement, label: string): void {
  const original = button.dataset.original ?? button.textContent ?? "复制";
  button.dataset.original = original;
  button.textContent = label;
  button.classList.add("copied");
  window.setTimeout(() => {
    button.textContent = button.dataset.original ?? "复制";
    button.classList.remove("copied");
  }, 1400);
}

function escapeHtml(value: string): string {
  return escapeCodeHTML(value);
}

function escapeAttr(value: string): string {
  return escapeHtml(value).replace(/"/g, "&quot;");
}

function encodeCodePayload(code: string): string {
  try {
    // 先 encodeURIComponent 处理多字节，再 btoa 得到 ASCII 安全串。
    return btoa(unescape(encodeURIComponent(code)));
  } catch {
    return "";
  }
}

function decodeCodePayload(payload: string): string {
  try {
    return decodeURIComponent(escape(atob(payload)));
  } catch {
    return "";
  }
}
