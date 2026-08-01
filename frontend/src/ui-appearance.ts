export type UIFontPreset = "system" | "segoe" | "yahei" | "custom";
export type UIScaleMode = "auto" | "manual";
export type UIFontSmoothing = "auto" | "antialiased";

export type UIAppearancePreferences = {
  fontPreset: UIFontPreset;
  customFontFamily: string;
  codeFontFamily: string;
  fontSize: number;
  codeFontSize: number;
  lineHeight: number;
  scaleMode: UIScaleMode;
  manualScale: number;
  fontSmoothing: UIFontSmoothing;
};

export const uiAppearanceStorageKey = "echocode:ui-appearance-v1";

export const defaultUIAppearance: UIAppearancePreferences = {
  fontPreset: "system",
  customFontFamily: "",
  codeFontFamily: '"Cascadia Mono", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
  fontSize: 14,
  codeFontSize: 13,
  lineHeight: 1.55,
  scaleMode: "auto",
  manualScale: 100,
  fontSmoothing: "auto",
};

export const uiFontPresetOptions: Array<{ value: UIFontPreset; label: string }> = [
  { value: "system", label: "跟随系统" },
  { value: "segoe", label: "Segoe UI" },
  { value: "yahei", label: "Microsoft YaHei UI" },
  { value: "custom", label: "自定义字体栈" },
];

const fontStacks: Record<Exclude<UIFontPreset, "custom">, string> = {
  system: 'system-ui, "Segoe UI Variable Text", "Segoe UI Variable", "Segoe UI", "Microsoft YaHei UI", "Microsoft YaHei", sans-serif',
  segoe: '"Segoe UI Variable Text", "Segoe UI Variable", "Segoe UI", sans-serif',
  yahei: '"Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI", sans-serif',
};

export function normalizeUIAppearance(value: Partial<UIAppearancePreferences> | undefined): UIAppearancePreferences {
  const fontPreset: UIFontPreset = value?.fontPreset === "segoe" || value?.fontPreset === "yahei" || value?.fontPreset === "custom"
    ? value.fontPreset
    : "system";
  const scaleMode: UIScaleMode = value?.scaleMode === "manual" ? "manual" : "auto";
  const fontSmoothing: UIFontSmoothing = value?.fontSmoothing === "antialiased" ? "antialiased" : "auto";
  return {
    fontPreset,
    customFontFamily: sanitizeFontStack(value?.customFontFamily ?? ""),
    codeFontFamily: sanitizeFontStack(value?.codeFontFamily ?? "") || defaultUIAppearance.codeFontFamily,
    fontSize: Math.round(clamp(Number(value?.fontSize) || defaultUIAppearance.fontSize, 12, 18)),
    codeFontSize: Math.round(clamp(Number(value?.codeFontSize) || defaultUIAppearance.codeFontSize, 11, 17)),
    lineHeight: Math.round(clamp(Number(value?.lineHeight) || defaultUIAppearance.lineHeight, 1.3, 2) * 100) / 100,
    scaleMode,
    manualScale: Math.round(clamp(Number(value?.manualScale) || defaultUIAppearance.manualScale, 80, 130)),
    fontSmoothing,
  };
}

export function readStoredUIAppearance(): UIAppearancePreferences {
  const raw = readLocalStorage(uiAppearanceStorageKey);
  if (!raw) return { ...defaultUIAppearance };
  try {
    return normalizeUIAppearance(JSON.parse(raw) as Partial<UIAppearancePreferences>);
  } catch {
    return { ...defaultUIAppearance };
  }
}

export function persistUIAppearance(preferences: UIAppearancePreferences): void {
  writeLocalStorage(uiAppearanceStorageKey, JSON.stringify(normalizeUIAppearance(preferences)));
}

export function resolveAutoUIScale(width: number, height: number): number {
  // WebView2 already follows the operating system DPI. Fractional CSS zoom in
  // automatic mode makes ClearType text smaller and visibly softer.
  void width;
  void height;
  return 1;
}

export function resolveEffectiveUIScale(
  preferences: UIAppearancePreferences,
  width = window.innerWidth,
  height = window.innerHeight,
): number {
  const normalized = normalizeUIAppearance(preferences);
  return normalized.scaleMode === "manual"
    ? normalized.manualScale / 100
    : resolveAutoUIScale(width, height);
}

export function applyUIAppearance(preferences: UIAppearancePreferences, shell?: HTMLElement): number {
  const normalized = normalizeUIAppearance(preferences);
  const scale = resolveEffectiveUIScale(normalized);
  const uiFont = normalized.fontPreset === "custom"
    ? normalized.customFontFamily || fontStacks.system
    : fontStacks[normalized.fontPreset];
  const layoutWidth = `${Math.ceil(window.innerWidth / scale)}px`;
  const layoutHeight = `${Math.ceil(window.innerHeight / scale)}px`;
  const smoothing = normalized.fontSmoothing === "antialiased" ? "antialiased" : "auto";

  const targets = [document.documentElement, shell].filter((target): target is HTMLElement => Boolean(target));
  for (const target of targets) {
    target.style.setProperty("--ui-font", uiFont);
    target.style.setProperty("--mono", normalized.codeFontFamily);
    target.style.setProperty("--ui-font-size", `${normalized.fontSize}px`);
    target.style.setProperty("--code-font-size", `${normalized.codeFontSize}px`);
    target.style.setProperty("--ui-line-height", String(normalized.lineHeight));
    target.style.setProperty("--ui-scale", String(scale));
    target.style.setProperty("--ui-layout-width", layoutWidth);
    target.style.setProperty("--ui-layout-height", layoutHeight);
    target.style.setProperty("--ui-font-smoothing", smoothing);
  }
  document.documentElement.dataset.uiScaleMode = normalized.scaleMode;
  return scale;
}

function sanitizeFontStack(value: string): string {
  return value
    .replace(/[\r\n;{}]/g, " ")
    .replace(/\s{2,}/g, " ")
    .trim()
    .slice(0, 240);
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function readLocalStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeLocalStorage(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable in a locked-down WebView; keep in-memory state.
  }
}
