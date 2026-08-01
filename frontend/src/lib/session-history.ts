import type { SessionMessage } from "../types";
import type { ChatMessage } from "../ui-types";
import type { ComposerSnapshot } from "./composer-history";

export type OptimisticTurnSnapshot = ComposerSnapshot & {
  userMessageID: string;
  assistantMessageID: string;
};

export type OptimisticTurnRollback = {
  messages: ChatMessage[];
  composer: ComposerSnapshot;
};

export function rollbackOptimisticTurnState(
  current: ChatMessage[],
  turn: OptimisticTurnSnapshot,
): OptimisticTurnRollback {
  const optimisticIDs = new Set([turn.userMessageID, turn.assistantMessageID].filter(Boolean));
  return {
    messages: current.filter((message) => !optimisticIDs.has(message.id)),
    composer: {
      draft: turn.draft,
      tail: turn.tail,
      attachments: turn.attachments.map((attachment) => ({ ...attachment })),
      links: turn.links.map((link) => ({ ...link })),
    },
  };
}

export function reconcileSessionMessages(
  current: ChatMessage[],
  history: SessionMessage[],
  preserveCurrentOnEmpty: boolean,
  fallbackTimestamp = Date.now(),
): ChatMessage[] {
  if (history.length === 0) {
    return preserveCurrentOnEmpty ? current : [];
  }

  return history.map((message, index) => ({
    id: message.id || `history-${index}-${fallbackTimestamp}`,
    eventId: message.id || undefined,
    role: normalizeHistoryRole(message.role),
    content: message.content,
    model: message.model,
    parts: message.parts,
    attachments: message.attachments,
    createdAt: message.createdAt || new Date(fallbackTimestamp).toISOString(),
    durationMs: message.durationMs,
	failed: message.status === "failed",
	cancelled: message.status === "cancelled",
	interrupted: message.status === "interrupted",
	status: message.status === "interrupted" ? "上次运行中断" : undefined,
	statusKind: message.status === "interrupted" ? "failed" : undefined,
  }));
}

function normalizeHistoryRole(role: string): ChatMessage["role"] {
  return role === "user" || role === "assistant" || role === "system" ? role : "assistant";
}
