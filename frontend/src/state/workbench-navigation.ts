import { batch, createMemo, createSignal } from "solid-js";
import type { SettingsCategory, ViewSnapshot, WorkbenchSection } from "../ui-types";

const extensionPages = new Set<SettingsCategory>(["extensions", "mcp", "plugins", "skills"]);

export function sectionForCategory(category: SettingsCategory): Exclude<WorkbenchSection, "chat"> {
  if (category === "automation") return "automation";
  return extensionPages.has(category) ? "extensions" : "settings";
}

export function createWorkbenchNavigation() {
  const [history, setHistory] = createSignal<ViewSnapshot[]>([{ drawer: false, category: "appearance" }]);
  const [cursor, setCursor] = createSignal(0);
  const [lastPreference, setLastPreference] = createSignal<SettingsCategory>("appearance");
  const view = createMemo(() => history()[cursor()]);
  const drawerOpen = createMemo(() => view().drawer);
  const activeSettingsCategory = createMemo(() => view().category);
  const activeSection = createMemo<WorkbenchSection>(() => view().drawer ? sectionForCategory(view().category) : "chat");
  const canGoBack = createMemo(() => cursor() > 0);
  const canGoForward = createMemo(() => cursor() < history().length - 1);

  function navigate(next: ViewSnapshot) {
    const current = view();
    if (current.drawer === next.drawer && (!next.drawer || current.category === next.category)) return;
    batch(() => {
      const entries = [...history().slice(0, cursor() + 1), next];
      setHistory(entries);
      setCursor(entries.length - 1);
      if (next.drawer && sectionForCategory(next.category) === "settings") setLastPreference(next.category);
    });
  }

  return {
    drawerOpen,
    activeSettingsCategory,
    activeSection,
    canGoBack,
    canGoForward,
    openSettings: (category: SettingsCategory = lastPreference()) => navigate({ drawer: true, category }),
    closeDrawer: () => navigate({ drawer: false, category: activeSettingsCategory() }),
    navigateBack: () => { if (canGoBack()) setCursor(cursor() - 1); },
    navigateForward: () => { if (canGoForward()) setCursor(cursor() + 1); },
  };
}
