import hljs from "highlight.js/lib/common";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import makefile from "highlight.js/lib/languages/makefile";
import powershell from "highlight.js/lib/languages/powershell";

if (!hljs.getLanguage("dockerfile")) hljs.registerLanguage("dockerfile", dockerfile);
if (!hljs.getLanguage("makefile")) hljs.registerLanguage("makefile", makefile);
if (!hljs.getLanguage("powershell")) hljs.registerLanguage("powershell", powershell);

const languageAliases: Record<string, string> = {
  bat: "dos",
  cc: "cpp",
  cmd: "dos",
  cs: "csharp",
  h: "cpp",
  hpp: "cpp",
  htm: "xml",
  html: "xml",
  js: "javascript",
  jsx: "javascript",
  md: "markdown",
  mjs: "javascript",
  ps1: "powershell",
  py: "python",
  rb: "ruby",
  sh: "bash",
  ts: "typescript",
  tsx: "typescript",
  vue: "xml",
  yml: "yaml",
};

const fileNameLanguages: Record<string, string> = {
  dockerfile: "dockerfile",
  makefile: "makefile",
};

export type HighlightedCode = {
  html: string;
  language: string;
};

export function normalizeCodeLanguage(value: string): string {
  const clean = value.trim().toLowerCase().replace(/^language-/, "");
  if (!clean) return "";
  const candidate = languageAliases[clean] ?? clean;
  return hljs.getLanguage(candidate) ? candidate : "";
}

export function codeLanguageForPath(path: string): string {
  const normalized = path.replaceAll("\\", "/").toLowerCase();
  const name = normalized.split("/").at(-1) ?? "";
  const byName = fileNameLanguages[name];
  if (byName) return normalizeCodeLanguage(byName);
  const extension = name.includes(".") ? name.split(".").at(-1) ?? "" : "";
  return normalizeCodeLanguage(extension);
}

export function highlightCode(content: string, language = ""): string {
  if (!content) return "";
  const normalized = normalizeCodeLanguage(language);
  if (!normalized) return escapeCodeHTML(content);
  try {
    return hljs.highlight(content, { language: normalized, ignoreIllegals: true }).value;
  } catch {
    return escapeCodeHTML(content);
  }
}

export function highlightCodeBlock(content: string, languageHint = ""): HighlightedCode {
  const language = normalizeCodeLanguage(languageHint);
  if (language) return { html: highlightCode(content, language), language };
  if (!content.trim()) return { html: escapeCodeHTML(content), language: "" };
  try {
    const detected = hljs.highlightAuto(content);
    return { html: detected.value, language: normalizeCodeLanguage(detected.language ?? "") };
  } catch {
    return { html: escapeCodeHTML(content), language: "" };
  }
}

export function escapeCodeHTML(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
