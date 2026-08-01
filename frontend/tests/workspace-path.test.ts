import { describe, expect, test } from "bun:test";

import { parseWorkspaceFileRangeCandidate, parseWorkspacePathCandidate } from "../src/lib/workspace-path";

describe("workspace path detection", () => {
  test("recognizes common relative and absolute source paths", () => {
    expect(parseWorkspacePathCandidate("index.html")).toEqual({ path: "index.html", line: undefined });
    expect(parseWorkspacePathCandidate("frontend/src/app.tsx:3186")).toEqual({ path: "frontend/src/app.tsx", line: 3186 });
    expect(parseWorkspacePathCandidate("C:/work/EchoCode/app.go#L42")).toEqual({ path: "C:/work/EchoCode/app.go", line: 42 });
  });

  test("does not turn URLs or ordinary inline code into files", () => {
    expect(parseWorkspacePathCandidate("https://example.com/index.html")).toBeUndefined();
    expect(parseWorkspacePathCandidate("72px")).toBeUndefined();
    expect(parseWorkspacePathCandidate("v0.2.0")).toBeUndefined();
    expect(parseWorkspacePathCandidate("scrollToBottom()")).toBeUndefined();
  });
});

describe("parseWorkspaceFileRangeCandidate", () => {
  test("parses read_file display ranges", () => {
    expect(parseWorkspaceFileRangeCandidate("frontend/src/app.tsx:3186-3230")).toEqual({
      path: "frontend/src/app.tsx",
      startLine: 3186,
      endLine: 3230,
    });
  });

  test("parses structured tool input", () => {
    expect(parseWorkspaceFileRangeCandidate('{"path":"internal/tools/file_read.go","start_line":46,"end_line":95}')).toEqual({
      path: "internal/tools/file_read.go",
      startLine: 46,
      endLine: 95,
    });
  });
});
