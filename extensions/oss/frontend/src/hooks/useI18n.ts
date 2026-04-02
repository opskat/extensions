import { useState, useEffect } from "react";

interface I18nStub {
  t: (key: string) => string;
  language: string;
}

/**
 * Hook that reads translations from the host-injected i18n stub.
 * Listens for 'opskat-language-change' events to re-render on language switch.
 */
export function useI18n() {
  const [, forceUpdate] = useState(0);

  useEffect(() => {
    const handler = () => forceUpdate((n) => n + 1);
    window.addEventListener("opskat-language-change", handler);
    return () => window.removeEventListener("opskat-language-change", handler);
  }, []);

  const i18n = window.__OPSKAT_EXT__?.i18n as I18nStub | undefined;

  const t = (key: string, params?: Record<string, string | number>): string => {
    let result = i18n?.t(key) ?? key;
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        result = result.replace(`{${k}}`, String(v));
      }
    }
    return result;
  };

  return { t, language: i18n?.language ?? "en" };
}
