// UI 局部类型：仅前端渲染层使用，与后端 types.ts 的领域类型区分开。
import type { ChatAttachment, UsageMetrics, ModelProviderSetting, MessagePart } from "./types";

export type ChatMessage = {
  id: string;
  eventId?: string;
  role: "user" | "assistant" | "system";
  content: string;
  createdAt: string;
  durationMs?: number;
  model?: string;
  reasoning?: string;
  usage?: UsageMetrics;
  failed?: boolean;
  streaming?: boolean;
  cancelled?: boolean;
	interrupted?: boolean;
  status?: string;
  statusKind?: "compression" | "running" | "waiting" | "retrying" | "failed" | "cancelled" | "completed";
  compressionStatus?: "running" | "completed" | "error";
  parts?: MessagePart[];
  attachments?: ChatAttachment[];
};

export type DrawerTab = "settings" | "cache" | "context" | "tools";
export type ViewSnapshot = { drawer: boolean; category: SettingsCategory };
export type SidebarSession = { title: string; meta: string; active: boolean; dot: boolean; onClick: () => void };
export type ThemeMode = "dark" | "light";

export type SettingsCategory =
  | "general"
  | "appearance"
  | "config"
  | "profile"
  | "shortcuts"
	| "extensions"
  | "mcp"
	| "plugins"
  | "browser"
  | "computer"
  | "models"
  | "team"
  | "skills"
  | "commands"
  | "memory"
  | "index"
  | "usage"
  | "environment"
  | "git"
  | "automation"
  | "about"
  | "archive";

export type ProviderPreset = {
  id: string;
  name: string;
  protocol: ModelProviderSetting["protocol"];
  apiType: ModelProviderSetting["apiType"];
  baseUrl: string;
  balanceUrl?: string;
  contextWindowTokens?: number;
  note: string;
};
