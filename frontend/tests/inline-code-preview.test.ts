import { describe, expect, test } from "bun:test";
import { parseInlineCode } from "../src/lib/inline-code";

describe("inline read_file code preview", () => {
  test("preserves line numbers returned by read_file", () => {
    const parsed = parseInlineCode(" 8 | <style>\n 9 | body { color: red; }\n10 | </style>", 8);
    expect(parsed.rows).toEqual([
      { lineNumber: 8, content: "<style>" },
      { lineNumber: 9, content: "body { color: red; }" },
      { lineNumber: 10, content: "</style>" },
    ]);
  });

  test("adds requested line numbers and separates truncation notices", () => {
    const parsed = parseInlineCode("const a = 1;\nconst b = 2;\n... [仅返回前 600 行，请继续读取]", 41);
    expect(parsed.rows).toEqual([
      { lineNumber: 41, content: "const a = 1;" },
      { lineNumber: 42, content: "const b = 2;" },
    ]);
    expect(parsed.notice).toContain("仅返回前 600 行");
  });

  test("does not mistake valid code for a truncation notice", () => {
    const parsed = parseInlineCode("const values = [1, 2];\n... [values]\nreturn values;", 10);
    expect(parsed.notice).toBe("");
    expect(parsed.rows.map((row) => row.content)).toEqual([
      "const values = [1, 2];",
      "... [values]",
      "return values;",
    ]);
  });
});
