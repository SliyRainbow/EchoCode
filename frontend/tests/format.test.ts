import { describe, expect, test } from "bun:test";

import { renderMarkdown } from "../src/lib/markdown";
import { errorMessage } from "../src/lib/errors";
import { reasoningOptions } from "../src/state/reasoning";
import { inferModelContextWindow } from "../src/model-context";
import { defaultTeamSettings } from "../src/team-config";
import { formatElapsedDuration } from "../src/lib/duration";

describe("frontend fallback state", () => {
  test("formats durable task timings", () => {
    expect(formatElapsedDuration(12_450)).toBe("12s");
    expect(formatElapsedDuration(765_000)).toBe("12m 45s");
    expect(formatElapsedDuration(3_661_000)).toBe("1h 1m 1s");
  });
  test("keeps reasoning budgets explicit", () => {
    expect(reasoningOptions.find((item) => item.id === "max")?.budget.planner).toBe(true);
	const lowBudget = reasoningOptions.find((item) => item.id === "low")?.budget;
	expect(lowBudget?.contextPolicy).toBe("minimal");
	expect(lowBudget && "maxToolCalls" in lowBudget).toBe(false);
  });

  test("renders code and escapes raw HTML", () => {
    const html = renderMarkdown("<script>alert(1)</script>\n\n```ts\nconst x = 1;\n```");
    expect(html).not.toContain("<script>alert(1)</script>");
    expect(html).toContain('<details class="code-block"');
    expect(html).not.toContain('<details class="code-block" open');
    expect(html).toContain("hljs-keyword");
  });

  test("can expand code blocks for document-style views without changing chat defaults", () => {
    const html = renderMarkdown("```ts\nconst x = 1;\n```", { expandCodeBlocks: true });
    expect(html).toContain('<details class="code-block" open');
  });

  test("does not expose an expected running-task race as a global error", () => {
    expect(errorMessage(new Error("chat task is running; stop it before saving runtime settings"))).toBe("");
    expect(errorMessage(new Error("network failed"))).toBe("network failed");
  });

  test("uses exact model ids for context windows", () => {
    expect(inferModelContextWindow("gpt-5.4", "local")).toEqual({ tokens: 1_050_000, source: "catalog" });
    expect(inferModelContextWindow("gpt-5.2-chat-latest", "local")).toEqual({ tokens: 128_000, source: "catalog" });
    expect(inferModelContextWindow("gpt-5.6-sol-custom", "local")).toEqual({ tokens: 64 * 1024, source: "safe-default" });
    expect(inferModelContextWindow("proxy/gpt-5.4", "local", 32_768)).toEqual({ tokens: 32_768, source: "provider-default" });
    expect(inferModelContextWindow("grok-4.5", "openai-compatible")).toEqual({ tokens: 500_000, source: "catalog" });
    expect(inferModelContextWindow("grok-build-latest", "openai-compatible")).toEqual({ tokens: 500_000, source: "catalog" });
    expect(inferModelContextWindow("grok-latest", "openai-compatible")).toEqual({ tokens: 1_000_000, source: "catalog" });
    expect(inferModelContextWindow("grok-build-0.1", "openai-compatible")).toEqual({ tokens: 256_000, source: "catalog" });
    expect(inferModelContextWindow("grok-4.20-0309-non-reasoning", "openai-compatible")).toEqual({ tokens: 1_000_000, source: "catalog" });
    expect(inferModelContextWindow("grok-4.20-multi-agent", "openai-compatible")).toEqual({ tokens: 1_000_000, source: "catalog" });
    expect(inferModelContextWindow("grok-chat-fast", "openai-compatible")).toEqual({ tokens: 64 * 1024, source: "safe-default" });
    expect(inferModelContextWindow("claude-fable-5", "anthropic")).toEqual({ tokens: 64 * 1024, source: "safe-default" });
    expect(inferModelContextWindow("claude-opus-5-20260724", "anthropic-compatible")).toEqual({ tokens: 64 * 1024, source: "safe-default" });
    expect(inferModelContextWindow("claude-opus-4-5-20251101", "anthropic")).toEqual({ tokens: 200_000, source: "catalog" });
    expect(inferModelContextWindow("claude-unknown", "anthropic")).toEqual({ tokens: 64 * 1024, source: "safe-default" });
  });

  test("keeps the five team roles explicit and disabled by default", () => {
    const team = defaultTeamSettings();
    expect(team.enabled).toBe(false);
    expect(team.maxReviewRounds).toBe(1);
    expect(team.roles.map((role) => role.role)).toEqual(["planner", "implementer", "tester", "reviewer", "synthesizer"]);
  });
});
