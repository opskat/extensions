// Types of the host modules the page imports by name. They are not packages:
// hostExternals (vite.config.ts) turns each import into a read of
// window.__OPSKAT_EXT__.ui / .hostUI, so these declare only the subset used.

declare module "@opskat/ui" {
  import type { ComponentProps, ComponentType, MouseEvent, ReactNode } from "react";

  export function cn(...inputs: unknown[]): string;

  export const Button: ComponentType<
    ComponentProps<"button"> & {
      variant?: "default" | "destructive" | "outline" | "secondary" | "ghost" | "link";
      size?: "default" | "xs" | "sm" | "lg" | "icon" | "icon-xs" | "icon-sm" | "icon-lg";
    }
  >;
  export const Input: ComponentType<ComponentProps<"input">>;
  export const Switch: ComponentType<
    Omit<ComponentProps<"button">, "onChange"> & {
      checked?: boolean;
      onCheckedChange?: (checked: boolean) => void;
    }
  >;
  export const Select: ComponentType<{
    value?: string;
    onValueChange?: (value: string) => void;
    disabled?: boolean;
    children?: ReactNode;
  }>;
  export const SelectTrigger: ComponentType<ComponentProps<"button"> & { size?: "sm" | "default" }>;
  export const SelectValue: ComponentType<{ placeholder?: string }>;
  export const SelectContent: ComponentType<{ className?: string; children?: ReactNode }>;
  export const SelectItem: ComponentType<{ value: string; className?: string; children?: ReactNode }>;

  export function useResizeHandle(options: {
    defaultSize: number;
    minSize: number;
    maxSize: number;
    storageKey?: string;
  }): { size: number; isResizing: boolean; handleMouseDown: (e: MouseEvent) => void };
}

declare module "@opskat/host-ui" {
  import type { ComponentType } from "react";

  /** Monaco editor and model; typed loosely since monaco-editor is not a dependency here. */
  export interface MonacoEditor {
    addAction(action: { id: string; label: string; keybindings?: number[]; run: () => void }): unknown;
  }
  export interface Monaco {
    KeyMod: { CtrlCmd: number };
    KeyCode: { Enter: number };
  }

  export const version: string;
  export const CodeEditor: ComponentType<{
    value?: string;
    onChange?: (value: string) => void;
    language?: "sql" | "javascript" | "json" | "plaintext" | "shell" | "markdown" | "yaml";
    readOnly?: boolean;
    height?: string | number;
    fontSize?: number;
    placeholder?: string;
    onMount?: (editor: MonacoEditor, monaco: Monaco) => void;
    className?: string;
  }>;
  export const JsonTreeView: ComponentType<{ data: unknown; className?: string }>;
  export const QueryResultTable: ComponentType<{
    columns: string[];
    rows: Record<string, unknown>[];
    loading?: boolean;
    showRowNumber?: boolean;
    rowNumberOffset?: number;
    rowDensity?: "compact" | "default" | "comfortable";
    onSelectedCellChange?: (cell: { rowIdx: number; col: string } | null) => void;
    onSelectedRowsChange?: (rowIdxs: number[]) => void;
  }>;
}
