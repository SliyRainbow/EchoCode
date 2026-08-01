import { Check, ChevronDown, Copy, FileCode2, PanelRightOpen } from "lucide-solid";
import { For, Show, createMemo, createSignal } from "solid-js";
import { writeClipboardText } from "../../lib/clipboard";
import { codeLanguageForPath, highlightCode } from "../../lib/code-highlighting";
import { inlineDiffStats, parseInlineDiff } from "../../lib/inline-diff";

type InlineDiffPreviewProps = {
  path: string;
  patch: string;
  additions?: number;
  deletions?: number;
  onOpen?: () => void;
	expanded?: boolean;
	onExpandedChange?: (expanded: boolean) => void;
};

export function InlineDiffPreview(props: InlineDiffPreviewProps) {
  const [expanded, setExpanded] = createSignal(false);
  const [copied, setCopied] = createSignal(false);
  const rows = createMemo(() => parseInlineDiff(props.patch));
  const language = createMemo(() => codeLanguageForPath(props.path));
  const calculatedStats = createMemo(() => inlineDiffStats(props.patch));
	const isExpanded = () => props.expanded ?? expanded();
	const toggleExpanded = () => {
		const next = !isExpanded();
		if (props.onExpandedChange) props.onExpandedChange(next);
		else setExpanded(next);
	};
  const additions = () => props.additions ?? calculatedStats().additions;
  const deletions = () => props.deletions ?? calculatedStats().deletions;

  const copyPatch = async () => {
    try {
      await writeClipboardText(props.patch);
    } catch {
      return;
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  };

  return (
    <section class="op-inline-diff" classList={{ collapsed: !isExpanded() }} aria-label={`${props.path} 修改预览`}>
      <header class="op-inline-diff-toolbar">
        <button
          type="button"
          class="op-inline-diff-identity"
          disabled={!props.onOpen}
          title={props.onOpen ? `在右侧查看 ${props.path}` : props.path}
          onClick={() => props.onOpen?.()}
        >
          <FileCode2 size={14} aria-hidden="true" />
          <strong>{baseName(props.path)}</strong>
          <span class="op-diff-stat">
            <Show when={additions() > 0}><em class="add">+{additions()}</em></Show>
            <Show when={deletions() > 0}><em class="del">-{deletions()}</em></Show>
          </span>
        </button>
        <div class="op-inline-code-actions">
          <button type="button" title={copied() ? "已复制" : "复制修改"} aria-label="复制修改" onClick={() => void copyPatch()}>
            <Show when={copied()} fallback={<Copy size={13} />}><Check size={13} /></Show>
          </button>
          <Show when={props.onOpen}>
            <button type="button" title="在右侧修改查看器中打开" aria-label="在右侧打开" onClick={() => props.onOpen?.()}>
              <PanelRightOpen size={13} />
            </button>
          </Show>
          <button
            type="button"
            title={isExpanded() ? "收起修改" : "展开修改"}
            aria-label={isExpanded() ? "收起修改" : "展开修改"}
            aria-expanded={isExpanded()}
			onClick={toggleExpanded}
          >
            <ChevronDown class="op-inline-code-toggle" size={14} />
          </button>
        </div>
      </header>

	  <Show when={isExpanded()}>
        <div class="op-inline-diff-scroll" role="region" aria-label="文件修改" tabIndex={0}>
          <For each={rows()}>
            {(row) => (
              <div class="op-inline-diff-row" classList={{ [row.kind]: true }}>
                <Show when={row.kind !== "meta"} fallback={<code class="op-inline-diff-meta">{row.content || " "}</code>}>
                  <span class="op-inline-diff-number" aria-hidden="true">{row.oldLine ?? ""}</span>
                  <span class="op-inline-diff-number" aria-hidden="true">{row.newLine ?? ""}</span>
                  <span class="op-inline-diff-marker" aria-hidden="true">{row.marker}</span>
                  <code class="hljs" innerHTML={highlightCode(row.content || " ", language()) || "&nbsp;"} />
                </Show>
              </div>
            )}
          </For>
        </div>
      </Show>
    </section>
  );
}

function baseName(path: string): string {
  return path.replaceAll("\\", "/").split("/").at(-1) || path;
}
