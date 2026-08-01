import type { MessagePart } from "../types";
import type { ChatResult } from "../types";
import type { ChatMessage } from "../ui-types";

export function hasMeaningfulTurnOutput(
  result: Pick<ChatResult, "content" | "reasoning" | "parts" | "turnCommitted"> | undefined,
  liveMessage?: Pick<ChatMessage, "content" | "reasoning" | "parts">,
): boolean {
  if (result?.turnCommitted === true) {
    return true;
  }
  if (result?.turnCommitted === false) {
    return false;
  }
  if (result?.turnCommitted === undefined && hasMeaningfulOutput(result?.content, result?.reasoning, result?.parts)) {
    return true;
  }
  return hasMeaningfulOutput(liveMessage?.content, liveMessage?.reasoning, liveMessage?.parts);
}

function hasMeaningfulOutput(content?: string, reasoning?: string, parts?: MessagePart[]): boolean {
  void reasoning;
  return Boolean(content?.trim() || parts?.some(isMeaningfulPart));
}

function isMeaningfulPart(part: MessagePart): boolean {
  switch (part.kind) {
    case "text":
      return Boolean(part.text.trim());
    case "tool_call":
      return Boolean(part.name.trim() || part.input?.trim() || part.output?.trim());
    case "diff":
      return Boolean(part.path.trim() || part.patch.trim());
    case "file":
      return Boolean(part.path.trim());
    case "task_progress":
      return part.steps.length > 0 || Boolean(part.changedFiles);
    case "web_search_results":
      return part.sources.length > 0;
    case "team_role":
      return Boolean(part.role?.trim() || part.summary?.trim());
    case "subagent":
      return Boolean(part.taskId.trim() || part.summary?.trim());
    case "provider_notice":
      return false;
	case "timeline_note":
	  return false;
    case "secret_result":
      return Boolean(part.secretId.trim());
  }
}

export function hasUsablePartialResult(parts: MessagePart[] | undefined): boolean {
  return Boolean(parts?.some((part) => (
    part.kind === "web_search_results" && part.sources.length > 0
  ) || (
    part.kind === "tool_call" &&
    (part.name === "read_repository" || part.name === "read_webpage") &&
    part.status !== "error" &&
    Boolean(part.output?.trim())
  )));
}
