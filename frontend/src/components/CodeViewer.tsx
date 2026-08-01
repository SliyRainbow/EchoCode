import { LanguageDescription } from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import { oneDark } from "@codemirror/theme-one-dark";
import { EditorView } from "@codemirror/view";
import { basicSetup } from "codemirror";
import { createEffect, onCleanup, onMount } from "solid-js";

type CodeViewerProps = {
  content: string;
  path: string;
  line?: number;
  wrap: boolean;
  dark: boolean;
};

const viewerTheme = EditorView.theme({
  "&": {
    height: "100%",
    minHeight: "0",
    color: "var(--code-text)",
    backgroundColor: "var(--bg-main)",
    fontSize: "12px",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": {
    minHeight: "0",
    overflow: "auto",
    fontFamily: "var(--mono, ui-monospace, SFMono-Regular, Consolas, monospace)",
    lineHeight: "21px",
  },
  ".cm-content": {
    minWidth: "max-content",
    padding: "6px 0",
    caretColor: "var(--accent)",
  },
  ".cm-line": { padding: "0 14px 0 10px" },
  ".cm-gutters": {
    minWidth: "48px",
    color: "var(--text-muted)",
    backgroundColor: "var(--bg-card)",
    borderRight: "1px solid var(--border-color)",
  },
  ".cm-lineNumbers .cm-gutterElement": {
    minWidth: "47px",
    padding: "0 9px 0 5px",
  },
  ".cm-activeLine": {
    backgroundColor: "color-mix(in srgb, var(--accent) 10%, transparent)",
    boxShadow: "inset 3px 0 0 var(--accent)",
  },
  ".cm-activeLineGutter": {
    color: "var(--text-primary)",
    backgroundColor: "color-mix(in srgb, var(--accent) 10%, var(--bg-card))",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in srgb, var(--accent) 28%, transparent) !important",
  },
  ".cm-panels": {
    color: "var(--text-primary)",
    backgroundColor: "var(--bg-card)",
  },
  ".cm-panels.cm-panels-top": { borderBottom: "1px solid var(--border-color)" },
  ".cm-search": { display: "flex", flexWrap: "wrap", alignItems: "center", gap: "5px" },
  ".cm-search label": { color: "var(--text-secondary)", fontSize: "11px" },
  ".cm-textfield": {
    height: "25px",
    color: "var(--text-primary)",
    backgroundColor: "var(--bg-input)",
    border: "1px solid var(--border-color)",
    borderRadius: "4px",
  },
  ".cm-button": {
    minHeight: "25px",
    color: "var(--text-secondary)",
    backgroundImage: "none",
    backgroundColor: "var(--bg-hover)",
    border: "1px solid var(--border-color)",
    borderRadius: "4px",
    fontSize: "11px",
  },
});

export function CodeViewer(props: CodeViewerProps) {
  const language = new Compartment();
  const wrapping = new Compartment();
  const colorTheme = new Compartment();
  let host: HTMLDivElement | undefined;
  let view: EditorView | undefined;
  let currentPath = "";
  let languageRequest = 0;

  onMount(() => {
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: props.content,
        extensions: [
          basicSetup,
          EditorState.readOnly.of(true),
          EditorState.phrases.of({
            "Find": "查找",
            "Replace": "替换",
            "next": "下一个",
            "previous": "上一个",
            "all": "全部",
            "match case": "区分大小写",
            "regexp": "正则表达式",
            "by word": "全字匹配",
            "replace": "替换",
            "replace all": "全部替换",
            "close": "关闭",
            "Go to line": "跳转到行",
            "go": "跳转",
          }),
          EditorView.editable.of(false),
          EditorView.contentAttributes.of({ "aria-label": "只读代码查看器", tabindex: "0" }),
          language.of([]),
          wrapping.of(props.wrap ? EditorView.lineWrapping : []),
          colorTheme.of(themeExtensions(props.dark)),
        ],
      }),
    });
    currentPath = props.path;
    void loadLanguage(props.path);
    queueMicrotask(() => jumpToLine(props.line));
  });

  createEffect(() => {
    const content = props.content;
    const path = props.path;
    if (!view) return;
    if (view.state.doc.toString() !== content) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: content } });
    }
    if (currentPath !== path) {
      currentPath = path;
      void loadLanguage(path);
    }
    queueMicrotask(() => jumpToLine(props.line));
  });

  createEffect(() => {
    const wrap = props.wrap;
    view?.dispatch({ effects: wrapping.reconfigure(wrap ? EditorView.lineWrapping : []) });
  });

  createEffect(() => {
    const dark = props.dark;
    view?.dispatch({ effects: colorTheme.reconfigure(themeExtensions(dark)) });
  });

  createEffect(() => {
    const line = props.line;
    if (view && line) queueMicrotask(() => jumpToLine(line));
  });

  onCleanup(() => {
    languageRequest++;
    view?.destroy();
    view = undefined;
  });

  async function loadLanguage(path: string) {
    const request = ++languageRequest;
    const description = LanguageDescription.matchFilename(languages, path);
    if (!description) {
      view?.dispatch({ effects: language.reconfigure([]) });
      return;
    }
    try {
      const support = await description.load();
      if (view && request === languageRequest) {
        view.dispatch({ effects: language.reconfigure(support) });
      }
    } catch {
      if (view && request === languageRequest) {
        view.dispatch({ effects: language.reconfigure([]) });
      }
    }
  }

  function jumpToLine(line: number | undefined) {
    if (!view || !line || view.state.doc.lines === 0) return;
    const target = view.state.doc.line(Math.min(Math.max(1, line), view.state.doc.lines));
    view.dispatch({
      selection: { anchor: target.from },
      effects: EditorView.scrollIntoView(target.from, { y: "center" }),
    });
  }

  return <div class="review-code-viewer" ref={host} />;
}

function themeExtensions(dark: boolean): Extension {
  return dark ? [oneDark, viewerTheme] : viewerTheme;
}
