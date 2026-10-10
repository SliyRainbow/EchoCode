import type { JSX } from "solid-js";
import { BarChart3, Brain, CalendarClock, Database, Download, Folder, GitBranch, Globe2, Hash, Info, Keyboard, Monitor, Palette, Plug, Blocks, SlidersHorizontal, Users, Wrench } from "lucide-solid";
import type { ProviderPreset, SettingsCategory } from "./ui-types";

export const sandboxOptions = [
  { value: "read-only", label: "只读", description: "只允许读取项目内容" },
  { value: "workspace-write", label: "工作区写入", description: "允许修改当前工作区" },
  { value: "danger-full-access", label: "全权限", description: "不限制文件系统边界" },
];

export const filesystemOptions = [
  { value: "read-only", label: "只读" },
  { value: "workspace-write", label: "工作区" },
  { value: "unrestricted", label: "不限制" },
];

export const approvalOptions = [
  { value: "on-request", label: "请求批准（边界内直接执行）" },
  { value: "always", label: "每次询问" },
  { value: "never", label: "永不询问" },
];

export const toolResultOptions = [
  { value: "summary-first", label: "摘要优先" },
  { value: "balanced", label: "平衡" },
  { value: "raw-local", label: "原文仅本地" },
];

export const stablePrefixOptions = [
  { value: "reuse-prefix", label: "复用前缀" },
  { value: "stable-prefix", label: "稳定前缀" },
  { value: "strict-stable-prefix", label: "严格稳定" },
];

export const providerProtocolOptions = [
  { value: "deepseek-official", label: "DeepSeek 官方" },
  { value: "openai-compatible", label: "OpenAI 兼容" },
  { value: "anthropic-compatible", label: "Anthropic 兼容" },
  { value: "gemini", label: "Gemini" },
  { value: "local", label: "本地兼容" },
];

export const providerAPITypeOptions = [
  { value: "chat-completions", label: "Chat Completions" },
  { value: "responses", label: "Responses" },
  { value: "anthropic-messages", label: "Anthropic Messages" },
  { value: "gemini-generate-content", label: "Gemini Generate Content" },
];

export const providerReasoningProfileOptions = [
  { value: "auto", label: "自动识别（推荐）", protocols: ["*"] },
  { value: "none", label: "不发送推理参数", protocols: ["*"] },
  { value: "openai", label: "OpenAI 官方模型", protocols: ["openai-compatible", "local"] },
  { value: "openai-effort", label: "通用 reasoning_effort", protocols: ["openai-compatible", "local"] },
  { value: "xai", label: "xAI Grok", protocols: ["openai-compatible", "local"] },
  { value: "deepseek", label: "DeepSeek 推理", protocols: ["deepseek-official", "openai-compatible", "local"] },
  { value: "anthropic", label: "Anthropic Thinking", protocols: ["anthropic", "anthropic-compatible"] },
  { value: "gemini", label: "Gemini Thinking", protocols: ["gemini"] },
];

export const providerPresets: ProviderPreset[] = [
  {
    id: "deepseek",
    name: "DeepSeek 官方",
    protocol: "deepseek-official",
    apiType: "chat-completions",
    baseUrl: "https://api.deepseek.com",
    contextWindowTokens: 128000,
    note: "官方通道，优先用于缓存命中观测。",
  },
  {
    id: "openai-compatible",
    name: "OpenAI 兼容",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://api.openai.com/v1",
    note: "标准 /v1/models 与 /chat/completions。",
  },
  {
    id: "xai-official",
    name: "xAI 官方",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://api.x.ai/v1",
    note: "自动拉取 Grok 模型，并按 xAI 官方模型卡识别上下文长度。",
  },
  {
    id: "anthropic-official",
    name: "Anthropic 官方",
    protocol: "anthropic-compatible",
    apiType: "anthropic-messages",
    baseUrl: "https://api.anthropic.com",
    note: "Claude Messages API 原生协议。",
  },
  {
    id: "google-gemini",
    name: "Google Gemini",
    protocol: "gemini",
    apiType: "gemini-generate-content",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta",
    note: "Gemini generateContent 原生协议。",
  },
  {
    id: "glm-cn",
    name: "GLM CN API",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://open.bigmodel.cn/api/paas/v4",
    note: "智谱国内 OpenAI 兼容入口。",
  },
  {
    id: "zai-global",
    name: "Z.AI Global API",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://api.z.ai/api/paas/v4",
    note: "国际站 OpenAI 兼容入口。",
  },
  {
    id: "qwen-cn",
    name: "Qwen CN API",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    note: "通义千问兼容模式。",
  },
  {
    id: "kimi",
    name: "Kimi",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://api.moonshot.cn/v1",
    note: "Moonshot / Kimi 兼容入口。",
  },
  {
    id: "minimax",
    name: "MiniMax",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://api.minimax.chat/v1",
    note: "MiniMax OpenAI 兼容入口。",
  },
  {
    id: "huggingface-router",
    name: "HuggingFace Router",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://router.huggingface.co/v1",
    note: "HuggingFace Inference Router。",
  },
  {
    id: "nvidia-nim",
    name: "NVIDIA NIM",
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "https://integrate.api.nvidia.com/v1",
    note: "NIM OpenAI 兼容入口。",
  },
  {
    id: "ollama-local",
    name: "Ollama 本地",
    protocol: "local",
    apiType: "chat-completions",
    baseUrl: "http://127.0.0.1:11434/v1",
    note: "本地服务可无密钥拉取模型。",
  },
];

export const sitePermissionOptions = [
  { value: "ask", label: "询问" },
  { value: "allow", label: "允许" },
  { value: "block", label: "阻止" },
];

export const defaultSidebarWidth = 252;
export const minSidebarWidth = 220;
export const maxSidebarWidth = 420;
export const sidebarWidthStorageKey = "echocode:sidebar-width";
export const defaultBrowserPanelWidth = 720;
export const minBrowserPanelWidth = 420;
export const maxBrowserPanelWidth = 1600;
export const minChatPaneWidth = 360;
export const browserPanelWidthStorageKey = "echocode:browser-panel-width";
export const themeStorageKey = "echocode:theme";

export const settingsGroups: Array<{
  title: string;
  items: Array<{ id: SettingsCategory; label: string; icon: () => JSX.Element }>;
}> = [
  {
    title: "偏好",
    items: [
      { id: "appearance", label: "外观", icon: () => <Palette size={15} /> },
      { id: "shortcuts", label: "键盘快捷键", icon: () => <Keyboard size={15} /> },
    ],
  },
  {
    title: "模型与 Agent",
    items: [
      { id: "models", label: "模型与连接", icon: () => <Database size={15} /> },
      { id: "team", label: "AI 团队", icon: () => <Users size={15} /> },
      { id: "memory", label: "记忆", icon: () => <Brain size={15} /> },
    ],
  },
  {
    title: "权限",
    items: [
      { id: "config", label: "权限与执行", icon: () => <SlidersHorizontal size={15} /> },
      { id: "browser", label: "浏览器", icon: () => <Globe2 size={15} /> },
      { id: "computer", label: "电脑操控", icon: () => <Monitor size={15} /> },
    ],
  },
  { title: "工作区", items: [
    { id: "environment", label: "工作区与环境", icon: () => <Folder size={15} /> },
    { id: "git", label: "Git", icon: () => <GitBranch size={15} /> },
  ] },
  { title: "用量与诊断", items: [
    { id: "usage", label: "使用统计", icon: () => <BarChart3 size={15} /> },
    { id: "index", label: "上下文诊断", icon: () => <Hash size={15} /> },
  ] },
  {
    title: "应用",
    items: [
      { id: "about", label: "关于 EchoCode", icon: () => <Info size={15} /> },
    ],
  },
];

export const extensionNavigation: Array<{id: SettingsCategory; label: string; icon: () => JSX.Element}> = [
  { id: "extensions", label: "发现扩展", icon: () => <Download size={15} /> },
  { id: "mcp", label: "MCP 服务器", icon: () => <Plug size={15} /> },
  { id: "plugins", label: "插件", icon: () => <Blocks size={15} /> },
  { id: "skills", label: "技能与工具", icon: () => <Wrench size={15} /> },
];
export const automationNavigation = { id: "automation" as const, label: "自动化任务", icon: () => <CalendarClock size={15} /> };
