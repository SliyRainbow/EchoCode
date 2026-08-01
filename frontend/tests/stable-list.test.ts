import { describe, expect, test } from "bun:test";
import { createRoot, createSignal } from "solid-js";
import { createStableListViews } from "../src/lib/stable-list";

describe("stable list views", () => {
  test("keeps an item's identity while exposing streamed updates", () => {
    createRoot((dispose) => {
      const [items, setItems] = createSignal([{ id: "message-1", text: "a", running: true }]);
      const views = createStableListViews(items, (item) => item.id);
      const first = views()[0];

      setItems([{ id: "message-1", text: "ab", running: true }]);

      expect(views()[0]).toBe(first);
      expect(first.text).toBe("ab");
      expect(first.running).toBe(true);
      dispose();
    });
  });

  test("creates a new view only for a genuinely new item", () => {
    createRoot((dispose) => {
      const [items, setItems] = createSignal([{ id: "message-1", text: "a" }]);
      const views = createStableListViews(items, (item) => item.id);
      const first = views()[0];

      setItems([{ id: "message-1", text: "a" }, { id: "message-2", text: "b" }]);

      expect(views()[0]).toBe(first);
      expect(views()[1]).not.toBe(first);
      dispose();
    });
  });
});
