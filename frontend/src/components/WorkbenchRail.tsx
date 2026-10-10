import { Blocks, Clock3, MessageCircle, Moon, Settings2, Sun } from "lucide-solid";
import { Show } from "solid-js";
import type { SettingsCategory, WorkbenchSection } from "../ui-types";
import { TooltipButton } from "./ui/TooltipButton";
import { BrandMark } from "./BrandMark";

export function WorkbenchRail(props: {
  section: WorkbenchSection;
  dark: boolean;
  onChat: () => void;
  onSettings: (category?: SettingsCategory) => void;
  onToggleTheme: () => void;
}) {
  return (
    <nav class="workbench-rail" aria-label="工作台导航">
      <span class="rail-brand" role="img" aria-label="EchoCode" title="EchoCode"><BrandMark /></span>
      <span class="rail-brand-divider" aria-hidden="true" />
      <div class="rail-primary">
        <TooltipButton label="工作区" active={props.section === "chat"} onClick={props.onChat}>
          <MessageCircle size={19} strokeWidth={1.6} />
        </TooltipButton>
        <TooltipButton label="自动化任务" active={props.section === "automation"} onClick={() => props.onSettings("automation")}>
          <Clock3 size={19} strokeWidth={1.6} />
        </TooltipButton>
        <TooltipButton label="扩展与工具" active={props.section === "extensions"} onClick={() => props.onSettings("extensions")}>
          <Blocks size={19} strokeWidth={1.6} />
        </TooltipButton>
      </div>
      <div class="rail-bottom">
        <TooltipButton label={props.dark ? "切换浅色主题" : "切换深色主题"} onClick={props.onToggleTheme}>
          <Show when={props.dark} fallback={<Moon size={18} strokeWidth={1.6} />}><Sun size={18} strokeWidth={1.6} /></Show>
        </TooltipButton>
        <TooltipButton label="设置" active={props.section === "settings"} onClick={() => props.onSettings()}>
          <Settings2 size={19} strokeWidth={1.6} />
        </TooltipButton>
      </div>
    </nav>
  );
}
