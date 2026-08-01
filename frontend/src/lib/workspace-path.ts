export type WorkspacePathCandidate = {
  path: string;
  line?: number;
};

export type WorkspaceFileRangeCandidate = {
  path: string;
  startLine?: number;
  endLine?: number;
};

const workspaceExtensions = new Set([
  "bat", "c", "cc", "cfg", "cmd", "conf", "cpp", "cs", "css", "csv",
  "env", "go", "graphql", "h", "hpp", "htm", "html", "ini", "java", "js",
  "json", "jsx", "kt", "kts", "less", "lock", "lua", "md", "mjs", "php",
  "ps1", "py", "rb", "rs", "scss", "sh", "sql", "svelte", "swift", "toml",
  "ts", "tsx", "txt", "vue", "xml", "yaml", "yml",
]);

const workspaceBasenames = new Set([
  ".editorconfig", ".gitignore", ".npmrc", "dockerfile", "gemfile", "license",
  "makefile", "procfile", "readme", "go.mod", "go.sum", "package.json",
  "bun.lock", "bun.lockb", "cargo.toml", "cargo.lock",
]);

export function parseWorkspacePathCandidate(value: string): WorkspacePathCandidate | undefined {
  let candidate = decodePath(value.trim());
  if (!candidate || candidate.length > 1024 || /[\r\n<>|{}]/.test(candidate)) return undefined;
  candidate = candidate.replace(/^[`'"(]+|[`'"),.;]+$/g, "");
  if (!candidate || /^(?:https?|data|javascript):/i.test(candidate)) return undefined;

  let line: number | undefined;
  const hashLine = candidate.match(/#L(\d+)(?:C\d+)?$/i);
  if (hashLine) {
    line = Number(hashLine[1]);
    candidate = candidate.slice(0, hashLine.index);
  } else {
    const suffixLine = candidate.match(/:(\d+)(?::\d+)?$/);
    if (suffixLine) {
      line = Number(suffixLine[1]);
      candidate = candidate.slice(0, suffixLine.index);
    }
  }

  if (/^file:\/\//i.test(candidate)) {
    try {
      const url = new URL(candidate);
      candidate = decodeURIComponent(url.pathname);
      if (/^\/[a-z]:\//i.test(candidate)) candidate = candidate.slice(1);
    } catch {
      return undefined;
    }
  }
  candidate = candidate.replace(/^\.\//, "");
  if (!candidate || candidate.endsWith("/") || candidate.endsWith("\\")) return undefined;

  const normalized = candidate.replaceAll("\\", "/");
  const basename = normalized.split("/").at(-1)?.toLowerCase() ?? "";
  const dot = basename.lastIndexOf(".");
  const extension = dot > 0 ? basename.slice(dot + 1) : "";
  const hasDirectory = normalized.includes("/");
  if (!hasDirectory && !workspaceBasenames.has(basename) && !workspaceExtensions.has(extension)) {
    return undefined;
  }
  return { path: candidate, line: Number.isFinite(line) && line! > 0 ? line : undefined };
}

export function parseWorkspaceFileRangeCandidate(value: string): WorkspaceFileRangeCandidate | undefined {
  const input = value.trim();
  if (!input) return undefined;

  if (input.startsWith("{")) {
    try {
      const parsed = JSON.parse(input) as Record<string, unknown>;
      if (typeof parsed.path === "string") {
        const candidate = parseWorkspacePathCandidate(parsed.path);
        if (!candidate) return undefined;
        const startLine = positiveLine(parsed.start_line) ?? candidate.line;
        const endLine = positiveLine(parsed.end_line);
        return {
          path: candidate.path,
          startLine,
          endLine: endLine && (!startLine || endLine >= startLine) ? endLine : undefined,
        };
      }
    } catch {
      return undefined;
    }
  }

  const range = input.match(/^(.*):(\d+)-(\d+)$/);
  if (range) {
    const candidate = parseWorkspacePathCandidate(range[1]);
    const startLine = Number(range[2]);
    const endLine = Number(range[3]);
    if (!candidate || startLine < 1 || endLine < startLine) return undefined;
    return { path: candidate.path, startLine, endLine };
  }

  const candidate = parseWorkspacePathCandidate(input);
  return candidate ? { path: candidate.path, startLine: candidate.line } : undefined;
}

function decodePath(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function positiveLine(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}
