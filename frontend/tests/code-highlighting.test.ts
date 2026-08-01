import { describe, expect, test } from "bun:test";

import {
  codeLanguageForPath,
  highlightCode,
  highlightCodeBlock,
  normalizeCodeLanguage,
} from "../src/lib/code-highlighting";

describe("shared code highlighting", () => {
  test("uses the same aliases for markdown, read_file, and diff previews", () => {
    expect(normalizeCodeLanguage("ts")).toBe("typescript");
    expect(codeLanguageForPath("frontend/src/app.tsx")).toBe("typescript");
    expect(codeLanguageForPath("scripts/build.ps1")).toBe("powershell");
  });

  test("highlights explicit languages and safely escapes plain text", () => {
    expect(highlightCode("const value = 1;", "typescript")).toContain("hljs-keyword");
    expect(highlightCode("<script>", "not-a-language")).toBe("&lt;script&gt;");
    expect(highlightCodeBlock("const value = 1;", "ts").language).toBe("typescript");
  });
});
