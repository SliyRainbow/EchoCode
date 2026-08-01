import { describe, expect, test } from "bun:test";

import {
  clearGuidanceMessages,
  dequeueMessage,
  enqueueMessage,
  prioritizeMessage,
  removeMessage,
  takeMessageForEditing,
  type QueuedComposerMessage,
} from "../src/lib/message-queue";

function queued(id: string, guidance = false): QueuedComposerMessage {
  return {
    id,
    draft: `message ${id}`,
    tail: "",
    attachments: [],
    links: [],
    createdAt: "2026-07-19T00:00:00Z",
    guidance,
  };
}

describe("message queue", () => {
  test("appends normal messages in order", () => {
    const queue = enqueueMessage([queued("a")], queued("b"));
    expect(queue.map((message) => message.id)).toEqual(["a", "b"]);
  });

  test("keeps exactly one guidance message at the front", () => {
    const queue = prioritizeMessage(
      [queued("a", true), queued("b"), queued("c")],
      "c",
    );
    expect(queue.map((message) => message.id)).toEqual(["c", "a", "b"]);
    expect(queue.filter((message) => message.guidance).map((message) => message.id)).toEqual(["c"]);
  });

  test("inserting guidance clears an older guidance marker", () => {
    const queue = enqueueMessage([queued("a", true), queued("b")], queued("c", true));
    expect(queue.map((message) => [message.id, message.guidance])).toEqual([
      ["c", true],
      ["a", false],
      ["b", false],
    ]);
  });

  test("removes only the selected message", () => {
    const queue = removeMessage([queued("a"), queued("b"), queued("c")], "b");
    expect(queue.map((message) => message.id)).toEqual(["a", "c"]);
  });

  test("dequeues the guidance message first and preserves the rest", () => {
    const result = dequeueMessage([queued("guide", true), queued("a"), queued("b")]);
    expect(result.message?.id).toBe("guide");
    expect(result.queue.map((message) => message.id)).toEqual(["a", "b"]);
  });

  test("takes a queued message back into the editor", () => {
    const result = takeMessageForEditing([queued("a"), queued("edit"), queued("b")], "edit");
    expect(result.message?.draft).toBe("message edit");
    expect(result.queue.map((message) => message.id)).toEqual(["a", "b"]);
  });

  test("keeps the queue unchanged when an edit target no longer exists", () => {
    const queue = [queued("a")];
    const result = takeMessageForEditing(queue, "missing");
    expect(result.message).toBeUndefined();
    expect(result.queue).toBe(queue);
  });

  test("releases pending guidance without dropping queued messages", () => {
    const queue = clearGuidanceMessages([queued("guide", true), queued("next")]);
    expect(queue.map((message) => [message.id, message.guidance])).toEqual([
      ["guide", false],
      ["next", false],
    ]);
  });
});
