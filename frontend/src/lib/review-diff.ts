export type DiffLineKind = "add" | "delete" | "context" | "hunk" | "meta";

export type DiffSegment = {
  text: string;
  changed: boolean;
};

export type DiffLine = {
  kind: DiffLineKind;
  marker: string;
  text: string;
  oldLine?: number;
  newLine?: number;
  segments?: DiffSegment[];
};

export function parseUnifiedDiff(patch: string): DiffLine[] {
  if (!patch.trim()) return [];
  const rows: DiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;
  const lines = patch.replace(/\r\n/g, "\n").split("\n");
  if (lines.at(-1) === "") lines.pop();
  for (const raw of lines) {
    if (raw.startsWith("@@")) {
      const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
      if (match) {
        oldLine = Number(match[1]);
        newLine = Number(match[2]);
      }
      inHunk = true;
      rows.push({ kind: "hunk", marker: "", text: raw });
      continue;
    }
    if (!inHunk || raw.startsWith("diff --git") || raw.startsWith("index ") || raw.startsWith("--- ") || raw.startsWith("+++ ") || raw.startsWith("new file mode") || raw.startsWith("deleted file mode")) {
      rows.push({ kind: "meta", marker: "", text: raw });
      continue;
    }
    if (raw.startsWith("+")) {
      rows.push({ kind: "add", marker: "+", text: raw.slice(1), newLine });
      newLine++;
      continue;
    }
    if (raw.startsWith("-")) {
      rows.push({ kind: "delete", marker: "-", text: raw.slice(1), oldLine });
      oldLine++;
      continue;
    }
    if (raw.startsWith(" ")) {
      rows.push({ kind: "context", marker: " ", text: raw.slice(1), oldLine, newLine });
      oldLine++;
      newLine++;
      continue;
    }
    rows.push({ kind: "meta", marker: "", text: raw });
  }
  return rows;
}

export function decorateWordDifferences(rows: DiffLine[]): DiffLine[] {
  const next = rows.map((row) => ({ ...row }));
  let index = 0;
  while (index < next.length) {
    if (next[index].kind !== "delete") {
      index++;
      continue;
    }
    const deletions: number[] = [];
    while (index < next.length && next[index].kind === "delete") deletions.push(index++);
    const additions: number[] = [];
    while (index < next.length && next[index].kind === "add") additions.push(index++);
    const pairs = Math.min(deletions.length, additions.length);
    for (let pair = 0; pair < pairs; pair++) {
      const oldRow = next[deletions[pair]];
      const newRow = next[additions[pair]];
      const [oldSegments, newSegments] = inlineDifference(oldRow.text, newRow.text);
      oldRow.segments = oldSegments;
      newRow.segments = newSegments;
    }
  }
  return next;
}

export function countDiffChanges(patch: string): { additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const line of patch.replace(/\r\n/g, "\n").split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) additions++;
    if (line.startsWith("-") && !line.startsWith("---")) deletions++;
  }
  return { additions, deletions };
}

function inlineDifference(before: string, after: string): [DiffSegment[], DiffSegment[]] {
  const left = Array.from(before);
  const right = Array.from(after);
  let prefix = 0;
  while (prefix < left.length && prefix < right.length && left[prefix] === right[prefix]) prefix++;
  let suffix = 0;
  while (suffix < left.length - prefix && suffix < right.length - prefix && left[left.length - 1 - suffix] === right[right.length - 1 - suffix]) suffix++;
  return [
    segmentsForDifference(left, prefix, suffix),
    segmentsForDifference(right, prefix, suffix),
  ];
}

function segmentsForDifference(value: string[], prefix: number, suffix: number): DiffSegment[] {
  const result: DiffSegment[] = [];
  if (prefix > 0) result.push({ text: value.slice(0, prefix).join(""), changed: false });
  const end = value.length - suffix;
  if (end > prefix) result.push({ text: value.slice(prefix, end).join(""), changed: true });
  if (suffix > 0) result.push({ text: value.slice(end).join(""), changed: false });
  return result.length ? result : [{ text: value.join(""), changed: false }];
}
