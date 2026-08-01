import type { Accessor } from "solid-js";

type StableListKey = string | number;

// Solid's <For> keys objects by reference. These proxies keep that reference stable
// while forwarding every property read to the newest version of the item.
export function createStableListViews<T extends object>(
  source: Accessor<readonly T[]>,
  keyOf: (item: T, index: number) => StableListKey,
): Accessor<T[]> {
  type Entry = { key: string; item: T };
  let lastSource: readonly T[] | undefined;
  let currentEntries: Entry[] = [];
  let latestByKey = new Map<string, T>();
  const views = new Map<string, T>();

  const refresh = (): Entry[] => {
    const current = source();
    if (current === lastSource) return currentEntries;
    lastSource = current;
    const occurrences = new Map<string, number>();
    currentEntries = current.map((item, index) => {
      const baseKey = String(keyOf(item, index));
      const occurrence = occurrences.get(baseKey) ?? 0;
      occurrences.set(baseKey, occurrence + 1);
      return { key: `${baseKey}\u0000${occurrence}`, item };
    });
    latestByKey = new Map(currentEntries.map((entry) => [entry.key, entry.item] as const));
    return currentEntries;
  };

  return () => {
    const entries = refresh();
    const activeKeys = new Set(entries.map((entry) => entry.key));
    for (const key of views.keys()) {
      if (!activeKeys.has(key)) views.delete(key);
    }

    return entries.map(({ key, item }) => {
      const existing = views.get(key);
      if (existing) return existing;

      const fallback = item;
      const view = new Proxy(item, {
        get: (_target, property) => {
          // Read the source in the property getter so Solid tracks streamed updates
          // even when <For> correctly reuses the same proxy object.
          refresh();
          const latest = latestByKey.get(key) ?? fallback;
          return Reflect.get(latest, property, latest);
        },
        has: (_target, property) => {
          refresh();
          return property in (latestByKey.get(key) ?? fallback);
        },
      });
      views.set(key, view);
      return view;
    });
  };
}
