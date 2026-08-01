import type { ChatAttachment } from "../types";

export type ComposerLink = {
  url: string;
  domain: string;
  label: string;
};

export type QueuedComposerMessage = {
  id: string;
  draft: string;
  tail: string;
  attachments: ChatAttachment[];
  links: ComposerLink[];
  createdAt: string;
  guidance: boolean;
};

export function enqueueMessage(
  queue: QueuedComposerMessage[],
  message: QueuedComposerMessage,
): QueuedComposerMessage[] {
  if (!message.guidance) return [...queue, message];
  return [message, ...queue.map((item) => ({ ...item, guidance: false }))];
}

export function prioritizeMessage(
  queue: QueuedComposerMessage[],
  id: string,
): QueuedComposerMessage[] {
  const selected = queue.find((message) => message.id === id);
  if (!selected) return queue;
  return [
    { ...selected, guidance: true },
    ...queue
      .filter((message) => message.id !== id)
      .map((message) => ({ ...message, guidance: false })),
  ];
}

export function clearGuidanceMessages(queue: QueuedComposerMessage[]): QueuedComposerMessage[] {
  return queue.map((message) => message.guidance ? { ...message, guidance: false } : message);
}

export function removeMessage(
  queue: QueuedComposerMessage[],
  id: string,
): QueuedComposerMessage[] {
  return queue.filter((message) => message.id !== id);
}

export function dequeueMessage(queue: QueuedComposerMessage[]): {
  message?: QueuedComposerMessage;
  queue: QueuedComposerMessage[];
} {
  const [message, ...remaining] = queue;
  return { message, queue: remaining };
}

export function takeMessageForEditing(
  queue: QueuedComposerMessage[],
  id: string,
): { message?: QueuedComposerMessage; queue: QueuedComposerMessage[] } {
  const message = queue.find((item) => item.id === id);
  if (!message) return { queue };
  return { message, queue: removeMessage(queue, id) };
}
