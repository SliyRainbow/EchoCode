import { Combobox } from "@kobalte/core/combobox";
import { Dialog } from "@kobalte/core/dialog";
import { Folder, MessageCircle, Search } from "lucide-solid";
import { Show, createMemo, createSignal } from "solid-js";
import type { ProjectNode } from "../types";

type SearchResult = { key: string; projectID: string; sessionID: string; title: string; projectName: string; updatedAt: string };

export function SessionSearch(props: {
  open: boolean;
  projects: ProjectNode[];
  onOpenChange: (open: boolean) => void;
  onSelect: (projectID: string, sessionID: string) => void;
}) {
  const [query, setQuery] = createSignal("");
  let input: HTMLInputElement | undefined;
  const results = createMemo<SearchResult[]>(() => props.projects.flatMap((project) => [
    { key: `project:${project.id}`, projectID: project.id, sessionID: "", title: project.name, projectName: "项目", updatedAt: "" },
    ...project.sessions.map((session) => ({ key: `${project.id}:${session.id}`, projectID: project.id, sessionID: session.id, title: session.title || "新对话", projectName: project.name + (session.archived ? " · 已归档" : ""), updatedAt: session.updatedAt })),
  ]).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)));
  const matches = (option: SearchResult, value: string) => `${option.title} ${option.projectName}`.toLocaleLowerCase().includes(value.trim().toLocaleLowerCase());
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay class="ui-modal-backdrop" />
        <div class="ui-modal-positioner search-positioner">
          <Dialog.Content class="session-search-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); input?.focus(); }}>
            <Dialog.Title class="sr-only">搜索对话与项目</Dialog.Title>
            <Dialog.Description class="sr-only">输入名称搜索，使用方向键选择并按 Enter 打开。</Dialog.Description>
            <Combobox<SearchResult>
              options={results()}
              optionValue="key"
              optionTextValue="title"
              defaultFilter={matches}
              open
              allowsEmptyCollection
              onInputChange={setQuery}
              onChange={(value) => {
                if (!value) return;
                props.onOpenChange(false);
                props.onSelect(value.projectID, value.sessionID);
              }}
              itemComponent={(item) => (
                <Combobox.Item item={item.item} class="session-search-result">
                  <Show when={item.item.rawValue.sessionID} fallback={<Folder size={16} />}><MessageCircle size={16} /></Show>
                  <div><Combobox.ItemLabel class="search-result-title">{item.item.rawValue.title}</Combobox.ItemLabel><Combobox.ItemDescription class="search-result-description">{item.item.rawValue.projectName}</Combobox.ItemDescription></div>
                </Combobox.Item>
              )}
            >
              <Combobox.Control class="session-search-input"><Search size={19} /><Combobox.Input ref={input} placeholder="搜索对话与项目…" aria-label="搜索对话与项目" /><Dialog.CloseButton aria-label="关闭搜索"><kbd>Esc</kbd></Dialog.CloseButton></Combobox.Control>
              <Combobox.Listbox class="session-search-results" aria-label="搜索结果" />
              <Show when={!results().some((option) => matches(option, query()))}><p class="session-search-empty">{query() ? "没有找到匹配的对话或项目" : "打开一个项目，开始你的第一段对话。"}</p></Show>
            </Combobox>
            <footer class="session-search-footer"><span><kbd>↑</kbd><kbd>↓</kbd> 选择</span><span><kbd>Enter</kbd> 打开</span></footer>
          </Dialog.Content>
        </div>
      </Dialog.Portal>
    </Dialog>
  );
}
