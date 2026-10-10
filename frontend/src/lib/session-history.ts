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

  const byEventID = new Map(current.map(message => [message.eventId || message.id, message]));
  const reconciled = history.map((message, index): ChatMessage => {
    const candidate = current[index];
    // Only adopt an optimistic identity at the same position in an unchanged
    // conversation. Branch changes never inherit another answer's UI state.
    const optimistic = preserveCurrentOnEmpty && candidate && !candidate.eventId
      && candidate.role === message.role && candidate.content === message.content
      && Boolean(message.content.trim()) ? candidate : undefined;
    const previous = byEventID.get(message.id) ?? optimistic;
    return {
      id: previous?.id || message.id || `history-${index}-${fallbackTimestamp}`,
      eventId: message.id || undefined,
      role: normalizeHistoryRole(message.role),
      content: message.content,
      reasoning: message.reasoning ?? previous?.reasoning,
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
    };
  });
  if (preserveCurrentOnEmpty) {
    // A failed disk write is not evidence that the visible answer was deleted.
    // Keep it through refresh/session switching until durable history catches up.
    const restoredIDs = new Set(reconciled.map(message => message.id));
    reconciled.push(...current.filter(message => message.historyError && !restoredIDs.has(message.id)));
  }
  return reconciled;
}

function normalizeHistoryRole(role: string): ChatMessage["role"] {
  return role === "user" || role === "assistant" || role === "system" ? role : "assistant";
}
