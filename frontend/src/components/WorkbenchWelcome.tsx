import { ArrowUpRight, FolderOpen, GitBranch, Search, Wrench } from "lucide-solid";
import { Show } from "solid-js";
import { BrandMark } from "./BrandMark";

export function WorkbenchWelcome(props: {
  workspaceRoot: string;
  workspaceName: string;
  onAddProject: () => void;
  onPrompt: (prompt: string) => void;
}) {
  return (
    <div class="welcome-state">
      <div class="welcome-identity">
        <BrandMark class="welcome-symbol" size={42} strokeWidth={1.35} />
        <h1>今天想做些什么？</h1>
        <Show when={props.workspaceRoot}>
          <p class="welcome-workspace" title={props.workspaceRoot}><FolderOpen size={14} />{props.workspaceName}</p>
        </Show>
      </div>
      <div class="welcome-suggestions" aria-label="开始一个任务">
        <Show when={!props.workspaceRoot}>
          <button type="button" onClick={props.onAddProject}><FolderOpen size={16} /><span>打开项目，接着上次的工作</span><ArrowUpRight size={14} /></button>
        </Show>
        <button type="button" onClick={() => props.onPrompt("梳理当前项目的结构、关键模块和运行方式")}><Search size={16} /><span>了解项目的结构与实现</span><ArrowUpRight size={14} /></button>
        <button type="button" onClick={() => props.onPrompt("检查当前工作区未提交的修改并指出风险")}><GitBranch size={16} /><span>检查最近的代码改动</span><ArrowUpRight size={14} /></button>
        <button type="button" onClick={() => props.onPrompt("定位当前项目的问题并给出修复方案")}><Wrench size={16} /><span>一起定位和解决问题</span><ArrowUpRight size={14} /></button>
      </div>
    </div>
  );
}
