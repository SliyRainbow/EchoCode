import { describe, expect, test } from "bun:test";
import { inlineDiffStats, parseInlineDiff } from "../src/lib/inline-diff";

describe("inline diff preview", () => {
  test("tracks old and new line numbers in unified hunks", () => {
    const rows = parseInlineDiff([
      "diff --git a/demo.ts b/demo.ts",
      "--- a/demo.ts",
      "+++ b/demo.ts",
      "@@ -7,2 +7,3 @@",
      " const first = 1;",
      "-const oldValue = 2;",
      "+const newValue = 2;",
      "+const extra = 3;",
    ].join("\n"));

    expect(rows.slice(-4)).toEqual([
      { kind: "context", content: "const first = 1;", marker: " ", oldLine: 7, newLine: 7 },
      { kind: "delete", content: "const oldValue = 2;", marker: "-", oldLine: 8 },
      { kind: "add", content: "const newValue = 2;", marker: "+", newLine: 8 },
      { kind: "add", content: "const extra = 3;", marker: "+", newLine: 9 },
    ]);
  });

  test("supports EchoCode simplified diffs without hunk headers", () => {
    const rows = parseInlineDiff("--- a/demo.txt\n+++ b/demo.txt\n-old\n+new\n same");
    expect(rows.slice(-3)).toEqual([
      { kind: "delete", content: "old", marker: "-", oldLine: 1 },
      { kind: "add", content: "new", marker: "+", newLine: 1 },
      { kind: "context", content: "same", marker: " ", oldLine: 2, newLine: 2 },
    ]);
  });

  test("derives change statistics when the backend omits them", () => {
    expect(inlineDiffStats("@@ -1,2 +1,3 @@\n-old\n+new\n+extra\n context")).toEqual({ additions: 2, deletions: 1 });
  });
});
