import { useCallback, useEffect, useRef, type Dispatch } from "react";
import { savedConsoles, type TabsAction, type TabsState } from "./es/tabs";
import { loadConsoles, saveConsoles } from "./host";

/** Edits are saved this long after the last one. */
const SAVE_DELAY_MS = 600;

/**
 * Keeps the page's console tabs and their text in the extension's storage, per
 * asset: restores them when the page opens, then saves shortly after every
 * change and once more when the page closes. Returns the consoles' text-change
 * handler. Index tabs are not kept.
 *
 * Nothing is saved until the saved tabs are loaded — otherwise a page that opens
 * would overwrite them with its own empty set — so if loading fails, this page
 * does not save at all, and says so in the log.
 */
export function useConsolePersistence(
  assetId: number,
  tabs: TabsState,
  dispatch: Dispatch<TabsAction>
): (id: string, text: string) => void {
  const texts = useRef(new Map<string, string>());
  const tabsRef = useRef(tabs);
  const loaded = useRef(false);
  const lastSaved = useRef("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const save = useCallback(() => {
    timer.current = null;
    const consoles = savedConsoles(tabsRef.current, texts.current);
    const json = JSON.stringify(consoles);
    if (json === lastSaved.current) return;
    lastSaved.current = json;
    saveConsoles(assetId, consoles).catch((err) => {
      lastSaved.current = ""; // the next change tries again
      console.error("[ext-elasticsearch] saving console tabs failed:", err);
    });
  }, [assetId]);

  const schedule = useCallback(() => {
    if (!loaded.current) return;
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(save, SAVE_DELAY_MS);
  }, [save]);

  useEffect(() => {
    let cancelled = false;
    loadConsoles(assetId).then(
      (consoles) => {
        if (cancelled) return;
        lastSaved.current = JSON.stringify(consoles);
        loaded.current = true;
        dispatch({ type: "restoreConsoles", consoles });
      },
      (err) => {
        if (!cancelled) console.error("[ext-elasticsearch] loading saved console tabs failed; not saving them:", err);
      }
    );
    return () => {
      cancelled = true;
    };
  }, [assetId, dispatch]);

  // Opening or closing a console changes what is saved.
  useEffect(() => {
    tabsRef.current = tabs;
    schedule();
  }, [tabs, schedule]);

  // Closing the page saves what is still waiting.
  useEffect(
    () => () => {
      if (timer.current) {
        clearTimeout(timer.current);
        save();
      }
    },
    [save]
  );

  return useCallback(
    (id: string, text: string) => {
      texts.current.set(id, text);
      schedule();
    },
    [schedule]
  );
}
