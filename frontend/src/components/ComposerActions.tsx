import { DropdownMenu } from "@kobalte/core/dropdown-menu";
import { For, onCleanup, Show } from "solid-js";
import { Check, ChevronDown, ClipboardList, FileText, ImagePlus, Plus, Redo2, ShieldCheck, SlidersHorizontal, Undo2, Users } from "lucide-solid";
import { permissionPresets, type PermissionMode, type PermissionPreset } from "../lib/permission-presets";

export function ComposerActions(props: {
  imagesDisabled: boolean;
  documentsDisabled: boolean;
  canUndo: boolean;
  canRedo: boolean;
  busy: boolean;
  plan: boolean;
  team: boolean;
  permission: PermissionMode;
  permissionsBusy: boolean;
  isolatedCommands: boolean;
  onImages: () => void;
  onDocuments: () => void;
  onUndo: () => void;
  onRedo: () => void;
  onPlan: () => void;
  onTeam: () => void;
  onPermission: (value: PermissionPreset) => void;
  onPermissionSettings: () => void;
  onOpenChange: (menu: string, open: boolean) => void;
}) {
  onCleanup(() => { props.onOpenChange("attachments", false); props.onOpenChange("mode", false); });
  return (
    <>
      <DropdownMenu placement="top-start" gutter={10} onOpenChange={(open) => props.onOpenChange("attachments", open)}>
        <DropdownMenu.Trigger class="composer-add" aria-label="添加附件与输入操作"><Plus size={20} strokeWidth={1.6} /></DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content class="ui-menu" aria-label="附件与输入操作">
            <DropdownMenu.Item class="ui-menu-item" disabled={props.imagesDisabled} onSelect={props.onImages}><ImagePlus size={16} /><span class="ui-menu-text">添加图片</span></DropdownMenu.Item>
            <DropdownMenu.Item class="ui-menu-item" disabled={props.documentsDisabled} onSelect={props.onDocuments}><FileText size={16} /><span class="ui-menu-text">添加 Markdown 资料</span></DropdownMenu.Item>
            <DropdownMenu.Separator class="ui-menu-separator" />
            <DropdownMenu.Item class="ui-menu-item" disabled={!props.canUndo} onSelect={props.onUndo}><Undo2 size={16} /><span class="ui-menu-text">撤销输入</span><kbd>Ctrl Z</kbd></DropdownMenu.Item>
            <DropdownMenu.Item class="ui-menu-item" disabled={!props.canRedo} onSelect={props.onRedo}><Redo2 size={16} /><span class="ui-menu-text">重做输入</span><kbd>Ctrl Y</kbd></DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu>
      <DropdownMenu placement="top-start" gutter={10} onOpenChange={(open) => props.onOpenChange("mode", open)}>
        <DropdownMenu.Trigger class="composer-mode" aria-label="任务模式与权限">
          <SlidersHorizontal size={14} /><span>{props.team ? "团队协作" : props.plan ? "先规划" : "默认模式"}</span><ChevronDown size={11} />
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content class="ui-menu" aria-label="任务模式与权限">
            <div class="ui-menu-label">任务模式</div>
            <DropdownMenu.CheckboxItem class="ui-menu-item" checked={props.plan} disabled={props.busy} onChange={props.onPlan}><ClipboardList size={16} /><span class="ui-menu-text">先规划，再执行</span><DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={14} aria-hidden="true" /></DropdownMenu.ItemIndicator></DropdownMenu.CheckboxItem>
            <DropdownMenu.CheckboxItem class="ui-menu-item" checked={props.team} disabled={props.busy} onChange={props.onTeam}><Users size={16} /><span class="ui-menu-text">AI 团队协作</span><DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={14} aria-hidden="true" /></DropdownMenu.ItemIndicator></DropdownMenu.CheckboxItem>
            <DropdownMenu.Separator class="ui-menu-separator" />
            <div class="ui-menu-label">工作权限</div>
            <DropdownMenu.RadioGroup value={props.permission} onChange={(value) => { if (value !== props.permission && permissionPresets.some(preset => preset.id === value)) props.onPermission(value as PermissionPreset); }}>
              <For each={permissionPresets}>{preset => (
                <DropdownMenu.RadioItem value={preset.id} class="ui-menu-item" disabled={props.permissionsBusy} closeOnSelect>
                  <ShieldCheck size={16} />
                  <div class="ui-menu-copy"><DropdownMenu.ItemLabel>{preset.label}</DropdownMenu.ItemLabel><DropdownMenu.ItemDescription class="ui-item-description">{preset.description}</DropdownMenu.ItemDescription></div>
                  <DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={14} aria-hidden="true" /></DropdownMenu.ItemIndicator>
                </DropdownMenu.RadioItem>
              )}</For>
            </DropdownMenu.RadioGroup>
            <DropdownMenu.Item class="ui-menu-item" onSelect={props.onPermissionSettings}><SlidersHorizontal size={16} /><span class="ui-menu-text">详细权限设置</span><Show when={props.permission === "custom"}><span class="ui-menu-indicator"><Check size={14} aria-label="当前使用自定义权限" /></span></Show></DropdownMenu.Item>
            <p class="ui-menu-help">网络与电脑操控仍由各自开关管理。</p>
            <Show when={props.permission === "workspace" && !props.isolatedCommands}><p class="ui-menu-help">当前系统尚无完整的文件与网络隔离，执行命令仍需批准。</p></Show>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu>
    </>
  );
}
