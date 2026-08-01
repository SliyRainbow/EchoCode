import type { MessagePart } from "../types";

const completePrivateReasoningBlock = /<(?:thinking|think|analysis|reasoning)\b[^>]*>[\s\S]*?<\/(?:thinking|think|analysis|reasoning)\s*>[\t ]*(?:\r?\n)?/gi;
const unfinishedPrivateReasoningBlock = /<(?:thinking|think|analysis|reasoning)\b[^>]*>[\s\S]*$/gi;
const orphanPrivateReasoningEnd = /<\/(?:thinking|think|analysis|reasoning)\s*>[\t ]*(?:\r?\n)?/gi;

export function sanitizeAssistantTextForDisplay(value: string | undefined): string {
  let visible = value?.trim() ?? "";
  for (;;) {
    const next = visible.replace(completePrivateReasoningBlock, "");
    if (next === visible) break;
    visible = next;
  }
  visible = visible
    .replace(unfinishedPrivateReasoningBlock, "")
    .replace(orphanPrivateReasoningEnd, "")
    .trim();
  return isProgressToolPayload(visible) ? "" : visible;
}

export function sanitizeAssistantMessageParts(parts: MessagePart[] | undefined): MessagePart[] {
  if (!parts?.length) return [];
  const visible: MessagePart[] = [];
  for (const part of parts) {
    if (part.kind !== "text") {
      visible.push(part);
      continue;
    }
    const text = sanitizeAssistantTextForDisplay(part.text);
    if (!text) continue;
    visible.push({ ...part, text });
  }
  return visible;
}

function isProgressToolPayload(text: string): boolean {
  try {
    const value = JSON.parse(text) as { message?: unknown; status?: unknown };
    if (!value || typeof value !== "object" || Array.isArray(value) || typeof value.message !== "string") return false;
    const keys = Object.keys(value);
    if (!keys.every((key) => key === "message" || key === "status")) return false;
    const status = typeof value.status === "string" ? value.status.trim() : "";
    return status === "" || ["running", "waiting", "retrying"].includes(status);
  } catch {
    return false;
  }
}
