// window.__OPSKAT_EXT__ as the host injects it (opskat
// frontend/src/extension/inject.ts), typed to the subset the page reads
// directly. React, @opskat/ui and @opskat/host-ui come off the same object
// through the build's hostExternals (vite.config.ts); see host-modules.d.ts.

export interface HostI18n {
  language: string;
  t(key: string, options?: Record<string, unknown>): string;
  on(event: "languageChanged", cb: (lng: string) => void): void;
  off(event: "languageChanged", cb: (lng: string) => void): void;
  addResourceBundle(lng: string, ns: string, resources: object, deep?: boolean, overwrite?: boolean): void;
}

export interface ExtAPI {
  callTool(
    extName: string,
    tool: string,
    args: unknown,
    assetId?: number,
    options?: { signal?: AbortSignal }
  ): Promise<unknown>;
}

declare global {
  interface Window {
    __OPSKAT_EXT__: {
      i18n: HostI18n;
      api: ExtAPI;
    };
  }
}

export {};
