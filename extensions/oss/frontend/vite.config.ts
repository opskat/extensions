import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/**
 * Vite plugin that replaces framework imports with host-provided instances.
 * Extensions run inside the OpsKat host which injects React, ReactDOM, i18n,
 * UI components and the extension API onto window.__OPSKAT_EXT__.
 */
function hostExternals(): Plugin {
  const modules: Record<string, string> = {
    react: [
      "const R = window.__OPSKAT_EXT__.React;",
      "export default R;",
      "export const {",
      "  useState, useEffect, useCallback, useMemo, useRef, useReducer,",
      "  useContext, createContext, createElement, Fragment, forwardRef,",
      "  memo, lazy, Suspense, Children, cloneElement, isValidElement,",
      "  startTransition",
      "} = R;",
    ].join("\n"),

    "react-dom": "export default window.__OPSKAT_EXT__.ReactDOM;",
    "react-dom/client": "export default window.__OPSKAT_EXT__.ReactDOM;",

    "react/jsx-runtime": [
      "const R = window.__OPSKAT_EXT__.React;",
      "export function jsx(type, props, key) {",
      "  if (key !== undefined) return R.createElement(type, { ...props, key });",
      "  return R.createElement(type, props);",
      "}",
      "export { jsx as jsxs };",
      "export const Fragment = R.Fragment;",
    ].join("\n"),

    "react/jsx-dev-runtime": [
      "const R = window.__OPSKAT_EXT__.React;",
      "export function jsxDEV(type, props, key, _isStatic) {",
      "  if (key !== undefined) return R.createElement(type, { ...props, key });",
      "  return R.createElement(type, props);",
      "}",
      "export const Fragment = R.Fragment;",
    ].join("\n"),

    "@opskat/ui": [
      "const _ui = window.__OPSKAT_EXT__.ui;",
      "export const {",
      "  cn, Button, buttonVariants, Input, Label,",
      "  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,",
      "  Switch, ScrollArea, ScrollBar,",
      "  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger,",
      "  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger,",
      "  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,",
      "  Tooltip, TooltipContent, TooltipProvider, TooltipTrigger,",
      "  Separator, ConfirmDialog, Textarea, useResizeHandle,",
      "  Popover, PopoverContent, PopoverTrigger,",
      "  Tabs, TabsContent, TabsList, TabsTrigger,",
      "  Card, CardContent, CardHeader, CardTitle,",
      "} = _ui;",
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
  plugins: [hostExternals(), react(), tailwindcss()],
  build: {
    lib: {
      entry: "src/index.ts",
      formats: ["es"],
      fileName: () => "index.js",
    },
    outDir: "../dist/frontend",
    emptyOutDir: true,
    cssCodeSplit: false,
    minify: false,
    rollupOptions: {
      output: {
        assetFileNames: "style.[ext]",
      },
    },
  },
});
