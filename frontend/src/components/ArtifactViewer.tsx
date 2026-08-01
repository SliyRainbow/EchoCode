import { ChevronLeft, ChevronRight, FileSpreadsheet, FileText, Presentation } from "lucide-solid";
import { For, Match, Show, Switch, createEffect, createMemo, createSignal } from "solid-js";
import type { JSX } from "solid-js";
import type { OfficeArtifactPreview } from "../types";

export function ArtifactViewer(props: { artifact: OfficeArtifactPreview }) {
  return (
    <Switch>
      <Match when={props.artifact.document}>
        {(document) => <DocumentArtifact document={document()} />}
      </Match>
      <Match when={props.artifact.spreadsheet}>
        {(spreadsheet) => <SpreadsheetArtifact spreadsheet={spreadsheet()} />}
      </Match>
      <Match when={props.artifact.presentation}>
        {(presentation) => <PresentationArtifact presentation={presentation()} />}
      </Match>
    </Switch>
  );
}

function DocumentArtifact(props: { document: NonNullable<OfficeArtifactPreview["document"]> }) {
  return (
    <div class="artifact-viewer document-artifact">
      <article class="artifact-document-page">
        <For each={props.document.blocks} fallback={<ArtifactEmpty icon={<FileText size={23} />} label="文档没有可显示的正文" />}>
          {(block) => (
            <Show when={block.type === "table"} fallback={<DocumentParagraph text={block.text ?? ""} style={block.style ?? "Normal"} />}>
              <div class="artifact-document-table-wrap">
                <table class="artifact-document-table">
                  <tbody>
                    <For each={block.table ?? []}>
                      {(row) => <tr><For each={row}>{(cell) => <td>{cell}</td>}</For></tr>}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
          )}
        </For>
      </article>
      <Show when={props.document.truncated}><div class="artifact-truncated-note">文档较长，侧栏仅显示前段内容</div></Show>
    </div>
  );
}

function DocumentParagraph(props: { text: string; style: string }) {
  const normalized = () => props.style.toLowerCase();
  return (
    <Switch fallback={<p>{props.text}</p>}>
      <Match when={normalized() === "title"}><h1>{props.text}</h1></Match>
      <Match when={normalized().includes("heading1") || normalized() === "heading 1"}><h2>{props.text}</h2></Match>
      <Match when={normalized().includes("heading2") || normalized() === "heading 2"}><h3>{props.text}</h3></Match>
      <Match when={normalized().includes("list")}><p class="artifact-list-paragraph">{props.text}</p></Match>
    </Switch>
  );
}

function SpreadsheetArtifact(props: { spreadsheet: NonNullable<OfficeArtifactPreview["spreadsheet"]> }) {
  const [activeName, setActiveName] = createSignal(props.spreadsheet.activeSheet || props.spreadsheet.sheets[0]?.name || "");
  createEffect(() => {
    const names = props.spreadsheet.sheets.map((sheet) => sheet.name);
    if (!names.includes(activeName())) setActiveName(props.spreadsheet.activeSheet || names[0] || "");
  });
  const activeSheet = createMemo(() => props.spreadsheet.sheets.find((sheet) => sheet.name === activeName()) ?? props.spreadsheet.sheets[0]);
  const visibleColumns = createMemo(() => Math.max(1, ...((activeSheet()?.rows ?? []).map((row) => row.length))));
  return (
    <div class="artifact-viewer spreadsheet-artifact">
      <div class="artifact-sheet-tabs" role="tablist" aria-label="工作表">
        <For each={props.spreadsheet.sheets} fallback={<span class="artifact-empty-inline"><FileSpreadsheet size={14} />没有工作表</span>}>
          {(sheet) => (
            <button type="button" role="tab" classList={{ active: activeSheet()?.name === sheet.name }} aria-selected={activeSheet()?.name === sheet.name} onClick={() => setActiveName(sheet.name)}>
              {sheet.name}
            </button>
          )}
        </For>
      </div>
      <Show when={activeSheet()} fallback={<ArtifactEmpty icon={<FileSpreadsheet size={23} />} label="工作簿没有可显示的数据" />}>
        {(sheet) => (
          <>
            <div class="artifact-sheet-meta">{sheet().rowCount} 行 × {sheet().columnCount} 列</div>
            <div class="artifact-grid-scroll">
              <table class="artifact-grid">
                <thead><tr><th class="row-number" /><For each={Array.from({ length: visibleColumns() })}>{(_, index) => <th>{columnLabel(index())}</th>}</For></tr></thead>
                <tbody>
                  <For each={sheet().rows} fallback={<tr><td class="artifact-grid-empty" colSpan={visibleColumns() + 1}>工作表为空</td></tr>}>
                    {(row, rowIndex) => (
                      <tr><th class="row-number">{rowIndex() + 1}</th><For each={Array.from({ length: visibleColumns() })}>{(_, columnIndex) => <td>{row[columnIndex()] ?? ""}</td>}</For></tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
            <Show when={sheet().truncated || props.spreadsheet.truncated}><div class="artifact-truncated-note">工作簿较大，侧栏显示已限制行列</div></Show>
          </>
        )}
      </Show>
    </div>
  );
}

function PresentationArtifact(props: { presentation: NonNullable<OfficeArtifactPreview["presentation"]> }) {
  const [selected, setSelected] = createSignal(0);
  createEffect(() => {
    if (selected() >= props.presentation.slides.length) setSelected(Math.max(0, props.presentation.slides.length - 1));
  });
  const slide = createMemo(() => props.presentation.slides[selected()]);
  return (
    <div class="artifact-viewer presentation-artifact">
      <Show when={slide()} fallback={<ArtifactEmpty icon={<Presentation size={23} />} label="演示文稿没有幻灯片" />}>
        {(current) => (
          <>
            <div class="artifact-slide-toolbar">
              <button type="button" title="上一页" disabled={selected() === 0} onClick={() => setSelected((value) => Math.max(0, value - 1))}><ChevronLeft size={15} /></button>
              <span>{current().number} / {props.presentation.slides.length}</span>
              <button type="button" title="下一页" disabled={selected() >= props.presentation.slides.length - 1} onClick={() => setSelected((value) => Math.min(props.presentation.slides.length - 1, value + 1))}><ChevronRight size={15} /></button>
            </div>
            <article class="artifact-slide-canvas">
              <h2>{current().title || `幻灯片 ${current().number}`}</h2>
              <div class="artifact-slide-body"><For each={current().texts}>{(text) => <p>{text}</p>}</For></div>
            </article>
            <div class="artifact-slide-strip" aria-label="幻灯片缩略图">
              <For each={props.presentation.slides}>
                {(item, index) => <button type="button" classList={{ active: selected() === index() }} title={item.title || `幻灯片 ${item.number}`} onClick={() => setSelected(index())}><span>{item.number}</span><strong>{item.title || "无标题"}</strong></button>}
              </For>
            </div>
            <Show when={props.presentation.truncated}><div class="artifact-truncated-note">演示文稿较长，侧栏仅加载前部分页面</div></Show>
          </>
        )}
      </Show>
    </div>
  );
}

function ArtifactEmpty(props: { icon: JSX.Element; label: string }) {
  return <div class="artifact-empty">{props.icon}<span>{props.label}</span></div>;
}

function columnLabel(index: number): string {
  let value = index + 1;
  let label = "";
  while (value > 0) {
    value--;
    label = String.fromCharCode(65 + (value % 26)) + label;
    value = Math.floor(value / 26);
  }
  return label;
}
