import { useCallback, useSyncExternalStore } from "react";
import en from "../../locales/en.json";
import zhCN from "../../locales/zh-CN.json";

// The page's strings live in the extension's locales (the same files the host
// loads as the `ext-elasticsearch` namespace). The host loads only the language
// active when the page first opens, so the page registers both bundles itself:
// switching language while it is open then re-renders in the new one.
export const NS = "ext-elasticsearch";

const i18n = () => window.__OPSKAT_EXT__.i18n;

export function registerLocales(): void {
  i18n().addResourceBundle("en", NS, en, true, false);
  i18n().addResourceBundle("zh-CN", NS, zhCN, true, false);
}

function subscribe(onChange: () => void): () => void {
  i18n().on("languageChanged", onChange);
  return () => i18n().off("languageChanged", onChange);
}

export type T = (key: string, options?: Record<string, unknown>) => string;

/** The page's translate function and the current language; re-renders on a language switch. */
export function useT(): { t: T; lang: string } {
  const lang = useSyncExternalStore(subscribe, () => i18n().language);
  // lang is a dependency on purpose: a new t is what makes memoised children re-render.
  const t = useCallback<T>((key, options) => i18n().t(key, { ns: NS, ...options }), [lang]);
  return { t, lang };
}
