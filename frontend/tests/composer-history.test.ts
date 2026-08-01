import { describe, expect, test } from "bun:test";

import {
  emptyComposerHistory,
  recordComposerSnapshot,
  redoComposerSnapshot,
  undoComposerSnapshot,
} from "../src/lib/composer-history";
import type { ComposerSnapshot } from "../src/lib/composer-history";

const snapshot = (draft: string): ComposerSnapshot => ({
  draft,
  tail: "",
  attachments: [],
  links: [],
});

describe("composer history", () => {
  test("undoes typing all the way back to an empty composer", () => {
    let history = emptyComposerHistory();
    history = recordComposerSnapshot(history, snapshot(""));
    history = recordComposerSnapshot(history, snapshot("a"));
    history = recordComposerSnapshot(history, snapshot("ab"));

    const first = undoComposerSnapshot(history, snapshot("abc"));
    expect(first.snapshot?.draft).toBe("ab");
    const second = undoComposerSnapshot(first.history, first.snapshot!);
    expect(second.snapshot?.draft).toBe("a");
    const third = undoComposerSnapshot(second.history, second.snapshot!);
    expect(third.snapshot?.draft).toBe("");
  });

  test("redoes an undone input state", () => {
    const history = recordComposerSnapshot(emptyComposerHistory(), snapshot(""));
    const undone = undoComposerSnapshot(history, snapshot("hello"));
    const redone = redoComposerSnapshot(undone.history, undone.snapshot!);
    expect(redone.snapshot?.draft).toBe("hello");
  });

  test("keeps links and attachments in the same undo snapshot", () => {
    const empty = snapshot("");
    const populated: ComposerSnapshot = {
      draft: "inspect",
      tail: "please",
      links: [{ url: "https://example.com", domain: "example.com", label: "example.com" }],
      attachments: [{ name: "image.png", mimeType: "image/png", data: "AAAA" }],
    };
    const history = recordComposerSnapshot(emptyComposerHistory(), empty);
    const undone = undoComposerSnapshot(history, populated);
    const redone = redoComposerSnapshot(undone.history, undone.snapshot!);
    expect(redone.snapshot).toEqual(populated);
  });
});
