import { describe, expect, test } from "bun:test";
import {
  countDiffChanges,
  decorateWordDifferences,
  parseUnifiedDiff,
} from "../src/lib/review-diff";

const patch = [
  "diff --git a/example.ts b/example.ts",
  "index 1111111..2222222 100644",
  "--- a/example.ts",
  "+++ b/example.ts",
  "@@ -1,2 +1,2 @@",
  "-const value = 1;",
  "+const value = 2;",
  " console.log(value);",
  "",
].join("\n");

describe("review diff parsing", () => {
  test("tracks old and new line numbers without a trailing phantom row", () => {
    const rows = parseUnifiedDiff(patch);
    expect(rows.at(-1)).toMatchObject({
      kind: "context",
      oldLine: 2,
      newLine: 2,
      text: "console.log(value);",
    });
    expect(rows.some((row) => row.kind === "meta" && row.text === "")).toBe(false);
  });

  test("marks the changed portion of paired replacement lines", () => {
    const rows = decorateWordDifferences(parseUnifiedDiff(patch));
    const deleted = rows.find((row) => row.kind === "delete");
    const added = rows.find((row) => row.kind === "add");
    expect(deleted?.segments?.some((segment) => segment.changed && segment.text === "1")).toBe(true);
    expect(added?.segments?.some((segment) => segment.changed && segment.text === "2")).toBe(true);
  });

  test("counts additions and deletions without treating file headers as changes", () => {
    expect(countDiffChanges(patch)).toEqual({ additions: 1, deletions: 1 });
  });
});
