import type { ChatAttachment } from "../types";
import type { ComposerLink } from "./message-queue";

export type ComposerSnapshot = {
  draft: string;
  tail: string;
  attachments: ChatAttachment[];
  links: ComposerLink[];
};

export type ComposerHistory = {
  past: ComposerSnapshot[];
  future: ComposerSnapshot[];
};

export type ComposerHistoryMove = {
  history: ComposerHistory;
  snapshot?: ComposerSnapshot;
};

export function emptyComposerHistory(): ComposerHistory {
  return { past: [], future: [] };
}

export function recordComposerSnapshot(
  history: ComposerHistory,
  current: ComposerSnapshot,
  limit = 200,
): ComposerHistory {
  const last = history.past.at(-1);
  if (last && composerSnapshotsEqual(last, current)) {
    return history.future.length === 0 ? history : { ...history, future: [] };
  }
  return {
    past: [...history.past, cloneComposerSnapshot(current)].slice(-Math.max(1, limit)),
    future: [],
  };
}

export function undoComposerSnapshot(
  history: ComposerHistory,
  current: ComposerSnapshot,
): ComposerHistoryMove {
  if (history.past.length === 0) return { history };
  const snapshot = history.past[history.past.length - 1];
  return {
    history: {
      past: history.past.slice(0, -1),
      future: [...history.future, cloneComposerSnapshot(current)],
    },
    snapshot: cloneComposerSnapshot(snapshot),
  };
}

export function redoComposerSnapshot(
  history: ComposerHistory,
  current: ComposerSnapshot,
): ComposerHistoryMove {
  if (history.future.length === 0) return { history };
  const snapshot = history.future[history.future.length - 1];
  return {
    history: {
      past: [...history.past, cloneComposerSnapshot(current)],
      future: history.future.slice(0, -1),
    },
    snapshot: cloneComposerSnapshot(snapshot),
  };
}

export function composerSnapshotsEqual(left: ComposerSnapshot, right: ComposerSnapshot): boolean {
  return left.draft === right.draft
    && left.tail === right.tail
    && left.links.length === right.links.length
    && left.links.every((link, index) => {
      const other = right.links[index];
      return Boolean(other)
        && link.url === other.url
        && link.domain === other.domain
        && link.label === other.label;
    })
    && left.attachments.length === right.attachments.length
    && left.attachments.every((attachment, index) => {
      const other = right.attachments[index];
      return Boolean(other)
        && attachment.name === other.name
        && attachment.mimeType === other.mimeType
        && attachment.data === other.data;
    });
}

function cloneComposerSnapshot(snapshot: ComposerSnapshot): ComposerSnapshot {
  return {
    draft: snapshot.draft,
    tail: snapshot.tail,
    attachments: snapshot.attachments.map((attachment) => ({ ...attachment })),
    links: snapshot.links.map((link) => ({ ...link })),
  };
}
