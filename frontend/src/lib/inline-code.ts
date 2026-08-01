export type InlineCodeRow = {
  lineNumber: number;
  content: string;
};

export type ParsedInlineCode = {
  rows: InlineCodeRow[];
  notice: string;
};

export function parseInlineCode(content: string, startLine = 1): ParsedInlineCode {
  const source = (content ?? "").replaceAll("\r\n", "\n").replaceAll("\r", "\n");
  const lines = source.split("\n");
  if (lines.length > 1 && lines.at(-1) === "") lines.pop();

  const numbered = lines.some((line) => /^\s*\d+\s*\|/.test(line));
  const rows: InlineCodeRow[] = [];
  let notice = "";
  let nextLine = Math.max(1, startLine || 1);

  for (const line of lines) {
    if (/^\.\.\.\s*\[仅返回前\s*\d+\s*行，请继续(?:从第\s*\d+\s*行)?读取\]$/.test(line.trim())) {
      notice = line.trim();
      continue;
    }
    const match = numbered ? line.match(/^\s*(\d+)\s*\|\s?(.*)$/) : undefined;
    if (match) {
      const lineNumber = Number(match[1]);
      rows.push({ lineNumber, content: match[2] });
      nextLine = lineNumber + 1;
      continue;
    }
    rows.push({ lineNumber: nextLine++, content: line });
  }

  if (rows.length === 0 && !notice) rows.push({ lineNumber: Math.max(1, startLine || 1), content: "" });
  return { rows, notice };
}
