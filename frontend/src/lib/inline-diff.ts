export type InlineDiffRow = {
  kind: "add" | "delete" | "context" | "meta";
  content: string;
  marker: string;
  oldLine?: number;
  newLine?: number;
};

export function parseInlineDiff(patch: string): InlineDiffRow[] {
  const source = (patch ?? "").replaceAll("\r\n", "\n").replaceAll("\r", "\n");
  const lines = source.split("\n");
  if (lines.length > 1 && lines.at(-1) === "") lines.pop();

  const rows: InlineDiffRow[] = [];
  let oldLine = 1;
  let newLine = 1;
  let inContent = false;

  for (const line of lines) {
    const hunk = line.match(/^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      inContent = true;
      rows.push(metaRow(line));
      continue;
    }
    if (line.startsWith("diff ") || line.startsWith("index ") || line.startsWith("--- ")) {
      rows.push(metaRow(line));
      continue;
    }
    if (line.startsWith("+++ ")) {
      rows.push(metaRow(line));
      inContent = true;
      continue;
    }
    if (!inContent || line.startsWith("\\ No newline") || line === "... [diff truncated]") {
      rows.push(metaRow(line));
      continue;
    }
    if (line.startsWith("+")) {
      rows.push({ kind: "add", content: line.slice(1), marker: "+", newLine: newLine++ });
      continue;
    }
    if (line.startsWith("-")) {
      rows.push({ kind: "delete", content: line.slice(1), marker: "-", oldLine: oldLine++ });
      continue;
    }

    const content = line.startsWith(" ") ? line.slice(1) : line;
    rows.push({ kind: "context", content, marker: " ", oldLine: oldLine++, newLine: newLine++ });
  }

  return rows;
}

export function inlineDiffStats(patch: string): { additions: number; deletions: number } {
  const rows = parseInlineDiff(patch);
  return {
    additions: rows.filter((row) => row.kind === "add").length,
    deletions: rows.filter((row) => row.kind === "delete").length,
  };
}

function metaRow(content: string): InlineDiffRow {
  return { kind: "meta", content, marker: "" };
}
