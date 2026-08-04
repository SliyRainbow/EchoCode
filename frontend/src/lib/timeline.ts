import type { ChatTaskEvent, MessagePart } from "../types";
import { sanitizeAssistantMessageParts, sanitizeAssistantTextForDisplay } from "./assistant-content";

export function appendLiveAssistantText(
  parts: MessagePart[] | undefined,
  content: string | undefined,
): MessagePart[] {
  const text = sanitizeAssistantTextForDisplay(content);
  if (!text) return parts ?? [];

  const next = [...(parts ?? [])];
  const last = next.at(-1);
  if (last?.kind === "text" && last.text.trim() === text) return next;
  next.push({ kind: "text", text });
  return next;
}

export function displayMessageParts(
  parts: MessagePart[] | undefined,
  content: string,
  streaming: boolean | undefined,
): MessagePart[] {
  const visibleParts = sanitizeAssistantMessageParts(parts);
  if (!visibleParts.length) {
    const visibleContent = sanitizeAssistantTextForDisplay(content);
    return visibleContent ? [{ kind: "text", text: visibleContent }] : [];
  }
  return streaming ? appendLiveAssistantText(visibleParts, content) : visibleParts;
}

export function updateLiveTimelineParts(
  parts: MessagePart[] | undefined,
  event: ChatTaskEvent,
): MessagePart[] {
  if (event.type === "heartbeat" || event.type === "started") return parts ?? [];

  const message = event.message?.trim();
  if (!message || isRoutineTaskStatus(message)) return parts ?? [];
  const status = event.type === "context_compression"
    ? event.compression?.status || event.status || "running"
    : event.status || "running";
  const next = [...(parts ?? [])];
	for (let index = next.length - 1; index >= 0; index--) {
	  const part = next[index];
	  if (part.kind !== "timeline_note") continue;
	  if (event.toolCallId && part.toolCallId === event.toolCallId || !event.toolCallId && part.message === message) {
		const currentTerminal = isTerminalTimelineStatus(part.status);
		const incomingTerminal = isTerminalTimelineStatus(status);
		next[index] = currentTerminal && !incomingTerminal
		  ? part
		  : {
			  ...part,
			  message,
			  status,
			  toolCallId: event.toolCallId || part.toolCallId,
			  durationMs: event.stageDurationMs ?? part.durationMs,
			  completedAt: incomingTerminal ? part.completedAt || new Date().toISOString() : part.completedAt,
			};
		return next;
	  }
	}
	const settled = settleLiveTimelineParts(next);
	settled.push({
	  kind: "timeline_note",
	  message,
	  status,
	  toolCallId: event.toolCallId,
	  durationMs: event.stageDurationMs,
	  startedAt: new Date().toISOString(),
	});
  return settled;
}

export function settleLiveTimelineParts(parts: MessagePart[] | undefined): MessagePart[] {
  return (parts ?? []).map((part) => part.kind === "timeline_note" && !isTerminalTimelineStatus(part.status)
	? { ...part, status: "completed", completedAt: part.completedAt || new Date().toISOString() }
	: part);
}

function isTerminalTimelineStatus(status: string | undefined): boolean {
  return ["completed", "failed", "cancelled", "interrupted", "error"].includes(status ?? "");
}

export function isRoutineTaskStatus(message: string | undefined): boolean {
	const normalized = message?.trim() ?? "";
	return normalized === "正在执行任务"
	  || normalized === "正在准备上下文"
    || normalized === "正在分析任务"
    || normalized === "正在生成执行计划"
    || normalized.startsWith("正在连接 ")
    || normalized.startsWith("上游模型仍在处理");
}

export function liveTaskStatus(message: string | undefined): string {
	void message;
	return "正在执行任务";
}
