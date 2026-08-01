import { describe, expect, test } from "bun:test";

import { normalizeUIAppearance, resolveAutoUIScale } from "../src/ui-appearance";

describe("UI appearance preferences", () => {
  test("normalizes persisted values into supported ranges", () => {
    const result = normalizeUIAppearance({
      fontPreset: "custom",
      customFontFamily: '"Example"; color: red',
      codeFontFamily: "",
      fontSize: 40,
      codeFontSize: 2,
      lineHeight: 9,
      scaleMode: "manual",
      manualScale: 500,
      fontSmoothing: "antialiased",
    });
    expect(result.fontSize).toBe(18);
    expect(result.codeFontSize).toBe(11);
    expect(result.lineHeight).toBe(2);
    expect(result.manualScale).toBe(130);
    expect(result.customFontFamily).not.toContain(";");
    expect(result.codeFontFamily.length).toBeGreaterThan(0);
  });

  test("keeps automatic scale at the native WebView DPI", () => {
    expect(resolveAutoUIScale(680, 900)).toBe(1);
    expect(resolveAutoUIScale(960, 700)).toBe(1);
    expect(resolveAutoUIScale(1280, 820)).toBe(1);
    expect(resolveAutoUIScale(2560, 1440)).toBe(1);
  });
});
