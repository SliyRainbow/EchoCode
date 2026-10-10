import type { RuntimeSettings } from "../types";

export type PermissionPreset = "read-only" | "workspace" | "full-access";
export type PermissionMode = PermissionPreset | "custom";

export const permissionPresets: Array<{ id: PermissionPreset; label: string; description: string }> = [
  { id: "workspace", label: "请求批准", description: "工作区内的文件修改直接执行，越界操作请求批准" },
  { id: "full-access", label: "完全访问", description: "不限制文件范围，已启用的工具无需逐次确认" },
  { id: "read-only", label: "只读", description: "检查项目文件；修改或执行前需切换权限" },
];

export function permissionMode(settings: RuntimeSettings): PermissionMode {
  if (settings.sandboxMode === "read-only" && settings.filesystemAccess === "read-only" && settings.approvalPolicy === "on-request") return "read-only";
  if (settings.sandboxMode === "workspace-write" && settings.filesystemAccess === "workspace-write" && settings.approvalPolicy === "on-request") return "workspace";
  if (settings.sandboxMode === "danger-full-access" && settings.filesystemAccess === "unrestricted" && settings.approvalPolicy === "never" && settings.networkAccess && settings.shellAccess && !settings.allowDestructiveOps) return "full-access";
  return "custom";
}

export function permissionLabel(settings: RuntimeSettings): string {
  return permissionPresets.find(preset => preset.id === permissionMode(settings))?.label ?? "自定义权限";
}

export function withPermissionPreset(settings: RuntimeSettings, preset: PermissionPreset): RuntimeSettings {
  // External applications, project roots and credentials retain their own
  // explicit controls. Presets never add a writable root or permitted app.
  if (preset === "read-only") return { ...settings, sandboxMode: "read-only", filesystemAccess: "read-only", approvalPolicy: "on-request" };
  if (preset === "workspace") return { ...settings, sandboxMode: "workspace-write", filesystemAccess: "workspace-write", approvalPolicy: "on-request", shellAccess: true };
  // Match the host invariant: unrestricted execution + no approvals cannot
  // enable the separate destructive file-operation override.
  return { ...settings, sandboxMode: "danger-full-access", filesystemAccess: "unrestricted", approvalPolicy: "never", networkAccess: true, shellAccess: true, allowDestructiveOps: false };
}
