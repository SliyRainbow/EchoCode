import { DropdownMenu } from "@kobalte/core/dropdown-menu";
import { Check, ChevronDown, Folder, FolderOpen } from "lucide-solid";
import { For } from "solid-js";
import type { ProjectNode } from "../types";

export function ProjectSwitcher(props: { projects: ProjectNode[]; activeID: string; name: string; busy: boolean; onSelect: (id: string) => void; onAdd: () => void; onOpenChange: (open: boolean) => void }) {
  return (
    <DropdownMenu placement="top-start" gutter={10} onOpenChange={props.onOpenChange}>
      <DropdownMenu.Trigger class="composer-project-trigger" aria-label="切换项目" disabled={props.busy}><Folder size={14} /><span>{props.name}</span><ChevronDown size={11} /></DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content class="ui-menu project-picker" aria-label="选择项目">
          <div class="ui-menu-label">项目</div>
          <DropdownMenu.RadioGroup value={props.activeID} onChange={id => { if (id !== props.activeID) props.onSelect(id); }}>
            <For each={props.projects} fallback={<p class="ui-menu-help">还没有添加项目。</p>}>{project => (
              <DropdownMenu.RadioItem class="ui-menu-item project-picker-option" value={project.id} closeOnSelect title={project.workspaceRoot}>
                <div class="ui-menu-copy"><DropdownMenu.ItemLabel>{project.name}</DropdownMenu.ItemLabel><DropdownMenu.ItemDescription class="ui-item-description">{project.workspaceRoot}</DropdownMenu.ItemDescription></div>
                <DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={14} /></DropdownMenu.ItemIndicator>
              </DropdownMenu.RadioItem>
            )}</For>
          </DropdownMenu.RadioGroup>
          <DropdownMenu.Separator class="ui-menu-separator" />
          <DropdownMenu.Item class="ui-menu-item" onSelect={props.onAdd}><FolderOpen size={15} /><span class="ui-menu-text">打开其他项目</span></DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu>
  );
}
