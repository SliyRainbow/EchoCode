import { describe, expect, test } from "bun:test";

import { reconcileSessionMessages, rollbackOptimisticTurnState } from "../src/lib/session-history";
import type { ChatMessage } from "../src/ui-types";

const current: ChatMessage[] = [
  {
    id: "local-user",
    role: "user",
    content: "keep this turn",
    createdAt: "2026-07-19T00:00:00.000Z",
  },
];

describe("session history reconciliation", () => {
  test("removes only the failed optimistic turn and restores a deep-cloned composer", () => {
    const attachment = { name: "screen.png", mimeType: "image/png", data: "base64-data" };
    const link = { url: "https://example.com", domain: "example.com", label: "Example" };
    const messages: ChatMessage[] = [
      current[0],
      { id: "optimistic-user", role: "user", content: "retry me", createdAt: "2026-07-19T00:00:01.000Z" },
      { id: "optimistic-assistant", role: "assistant", content: "", createdAt: "2026-07-19T00:00:02.000Z", streaming: true },
    ];

    const rollback = rollbackOptimisticTurnState(messages, {
      userMessageID: "optimistic-user",
      assistantMessageID: "optimistic-assistant",
      draft: "retry me",
      tail: "more context",
      attachments: [attachment],
      links: [link],
    });

    expect(rollback.messages).toEqual([current[0]]);
    expect(rollback.composer).toEqual({
      draft: "retry me",
      tail: "more context",
      attachments: [attachment],
      links: [link],
    });
    expect(rollback.composer.attachments[0]).not.toBe(attachment);
    expect(rollback.composer.links[0]).not.toBe(link);
  });

  test("keeps optimistic messages when a completion sync is temporarily empty", () => {
    expect(reconcileSessionMessages(current, [], true)).toBe(current);
  });

  test("clears messages when an explicit session change has no history", () => {
    expect(reconcileSessionMessages(current, [], false)).toEqual([]);
  });

  test("replaces optimistic messages with persisted history", () => {
    expect(reconcileSessionMessages(current, [
      {
        id: "event-1",
        role: "user",
        content: "persisted turn",
        createdAt: "2026-07-19T01:00:00.000Z",
      },
      {
        id: "event-2",
        role: "unexpected-role",
        content: "persisted reply",
        createdAt: "2026-07-19T01:00:01.000Z",
        durationMs: 12_450,
      },
    ], true)).toMatchObject([
      { id: "event-1", eventId: "event-1", role: "user", content: "persisted turn" },
      { id: "event-2", eventId: "event-2", role: "assistant", content: "persisted reply", durationMs: 12_450 },
    ]);
  });

	test("restores durable failed, cancelled, and interrupted task states", () => {
	  expect(reconcileSessionMessages([], [
		{ id: "failed", role: "assistant", content: "failed", createdAt: "2026-07-19T02:00:00.000Z", status: "failed" },
		{ id: "cancelled", role: "assistant", content: "stopped", createdAt: "2026-07-19T02:00:01.000Z", status: "cancelled" },
		{ id: "interrupted", role: "assistant", content: "partial", createdAt: "2026-07-19T02:00:02.000Z", status: "interrupted" },
	  ], false)).toMatchObject([
		{ id: "failed", failed: true, cancelled: false, interrupted: false },
		{ id: "cancelled", failed: false, cancelled: true, interrupted: false },
		{ id: "interrupted", failed: false, cancelled: false, interrupted: true, status: "上次运行中断", statusKind: "failed" },
	  ]);
	});
});
