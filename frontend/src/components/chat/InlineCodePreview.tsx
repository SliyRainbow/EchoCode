import { Check, ChevronDown, Copy, FileCode2, PanelRightOpen } from "lucide-solid";
import { For, Show, createMemo, createSignal } from "solid-js";
import { writeClipboardText } from "../../lib/clipboard";
import { codeLanguageForPath, highlightCodeBlock } from "../../lib/code-highlighting";
import { parseInlineCode } from "../../lib/inline-code";

type InlineCodePreviewProps = {
  path: string;
  content: string;
  startLine?: number;
  onOpen?: () => void;
	expanded?: boolean;
	onExpandedChange?: (expanded: boolean) => void;
};

export function InlineCodePreview(props: InlineCodePreviewProps) {
  const [expanded, setExpanded] = createSignal(false);
  const [copied, setCopied] = createSignal(false);
  const parsed = createMemo(() => parseInlineCode(props.content, props.startLine));
  const code = createMemo(() => parsed().rows.map((row) => row.content).join("\n"));
  const highlighted = createMemo(() => highlightCodeBlock(code(), codeLanguageForPath(props.path)));
	const isExpanded = () => props.expanded ?? expanded();
	const toggleExpanded = () => {
		const next = !isExpanded();
		if (props.onExpandedChange) props.onExpandedChange(next);
		else setExpanded(next);
	};
  const rangeLabel = createMemo(() => {
    const rows = parsed().rows;
    if (rows.length === 0) return "空文件";
    const first = rows[0].lineNumber;
    const last = rows.at(-1)?.lineNumber ?? first;
    return first === last ? `第 ${first} 行` : `${first}-${last} 行`;
  });

  const copyCode = async () => {
    try {
      await writeClipboardText(code());
    } catch {
      return;
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  };

  return (
    <section class="op-inline-code" classList={{ collapsed: !isExpanded() }} aria-label={`${props.path} 代码预览`}>
      <header class="op-inline-code-toolbar">
        <div class="op-inline-code-identity" title={props.path}>
          <FileCode2 size={14} aria-hidden="true" />
          <strong>{baseName(props.path)}</strong>
          <span>{rangeLabel()}</span>
          <span class="op-inline-code-language">{highlighted().language || "text"}</span>
        </div>
        <div class="op-inline-code-actions">
          <button type="button" title={copied() ? "已复制" : "复制代码"} aria-label="复制代码" onClick={() => void copyCode()}>
            <Show when={copied()} fallback={<Copy size={13} />}><Check size={13} /></Show>
          </button>
          <Show when={props.onOpen}>
            <button type="button" title="在右侧代码查看器中打开" aria-label="在右侧打开" onClick={() => props.onOpen?.()}>
              <PanelRightOpen size={13} />
            </button>
          </Show>
          <button
            type="button"
            title={isExpanded() ? "收起代码" : "展开代码"}
            aria-label={isExpanded() ? "收起代码" : "展开代码"}
            aria-expanded={isExpanded()}
			onClick={toggleExpanded}
          >
            <ChevronDown class="op-inline-code-toggle" size={14} />
          </button>
        </div>
      </header>

	  <Show when={isExpanded()}>
        <div class="op-inline-code-scroll" role="region" aria-label="代码内容" tabIndex={0}>
          <div class="op-inline-code-grid">
            <div class="op-inline-code-gutter" aria-hidden="true">
              <For each={parsed().rows}>{(row) => <span>{row.lineNumber}</span>}</For>
            </div>
            <pre><code class="hljs" innerHTML={highlighted().html || "&nbsp;"} /></pre>
          </div>
        </div>
        <Show when={parsed().notice}>
          <div class="op-inline-code-notice">{parsed().notice}</div>
        </Show>
      </Show>
    </section>
  );
}

function baseName(path: string): string {
  return path.replaceAll("\\", "/").split("/").at(-1) || path;
}
