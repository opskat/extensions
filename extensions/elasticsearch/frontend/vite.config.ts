import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/postcss";
import { scopeToPage } from "./tooling/scope-css";

/**
 * Resolves framework imports to what the host injects on window.__OPSKAT_EXT__
 * before it imports the page (opskat frontend/src/extension/inject.ts), so the
 * page runs on the host's own React, @opskat/ui and @opskat/host-ui instances —
 * same theme, same language, nothing duplicated in the bundle.
 */
function hostExternals(): Plugin {
  const modules: Record<string, string> = {
    react: [
      "const R = window.__OPSKAT_EXT__.React;",
      "export default R;",
      "export const {",
      "  useState, useEffect, useLayoutEffect, useCallback, useMemo, useRef, useReducer,",
      "  useContext, useId, useSyncExternalStore, useTransition, createContext, createElement,",
      "  Fragment, forwardRef, memo, lazy, Suspense, Children, cloneElement, isValidElement,",
      "  startTransition",
      "} = R;",
    ].join("\n"),

    "react-dom": "export default window.__OPSKAT_EXT__.ReactDOM;",
    "react-dom/client": "export default window.__OPSKAT_EXT__.ReactDOM;",

    // The automatic JSX runtime on createElement. jsxs gets a static children
    // list, which must go in as arguments: passed as a props array React would
    // take it for a dynamic list and warn about missing keys.
    "react/jsx-runtime": [
      "const R = window.__OPSKAT_EXT__.React;",
      "export function jsx(type, props, key) {",
      "  return R.createElement(type, key === undefined ? props : { ...props, key });",
      "}",
      "export function jsxs(type, props, key) {",
      "  const { children, ...rest } = props;",
      "  if (key !== undefined) rest.key = key;",
      "  return R.createElement(type, rest, ...children);",
      "}",
      "export const Fragment = R.Fragment;",
    ].join("\n"),

    "@opskat/ui": [
      "const _ui = window.__OPSKAT_EXT__.ui;",
      "export const {",
      "  cn, Button, Input, Switch, useResizeHandle,",
      "  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,",
      "} = _ui;",
    ].join("\n"),

    "@opskat/host-ui": [
      "const _h = window.__OPSKAT_EXT__.hostUI;",
      "export const { version, CodeEditor, JsonTreeView, QueryResultTable } = _h;",
    ].join("\n"),
  };

  return {
    name: "host-externals",
    enforce: "pre",
    resolveId(id) {
      if (id in modules) return `\0host:${id}`;
    },
    load(id) {
      if (id.startsWith("\0host:")) return modules[id.slice(6)];
    },
  };
}

export default defineConfig({
  plugins: [hostExternals(), react()],
  // Tailwind first, then scope what it generated to the page (tooling/scope-css.ts).
  css: { postcss: { plugins: [tailwindcss(), scopeToPage()] } },
  // Library mode leaves process.env.NODE_ENV for a bundler that never comes: the
  // host imports this file as-is.
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    lib: {
      entry: "src/index.ts",
      formats: ["es"],
      fileName: () => "index.js",
    },
    outDir: "../dist/frontend",
    emptyOutDir: true,
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        assetFileNames: "style.[ext]",
      },
    },
  },
});
