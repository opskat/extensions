/// <reference types="vite/client" />

import type React from "react";
import type ReactDOM from "react-dom/client";

interface ExtAPI {
  callTool(extName: string, tool: string, args: Record<string, unknown>): Promise<unknown>;
  executeAction(
    extName: string,
    action: string,
    args: Record<string, unknown>,
    onEvent?: (event: { eventType: string; data: unknown }) => void,
  ): Promise<unknown>;
}

declare global {
  interface Window {
    __OPSKAT_EXT__?: {
      React: typeof React;
      ReactDOM: typeof ReactDOM;
      i18n: unknown;
      ui: typeof import("@opskat/ui");
      api: ExtAPI;
    };
  }
}

// Type declarations for @opskat/ui virtual module.
// These match the host's @opskat/ui package exports.
declare module "@opskat/ui" {
  import type { ComponentType, ReactNode, HTMLAttributes, ButtonHTMLAttributes, InputHTMLAttributes } from "react";

  export function cn(...inputs: unknown[]): string;

  // Button
  export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: "default" | "destructive" | "outline" | "secondary" | "ghost" | "link";
    size?: "default" | "xs" | "sm" | "lg" | "icon" | "icon-xs" | "icon-sm" | "icon-lg";
    asChild?: boolean;
  }
  export const Button: ComponentType<ButtonProps>;
  export function buttonVariants(props?: Partial<ButtonProps>): string;

  // Input
  export const Input: ComponentType<InputHTMLAttributes<HTMLInputElement> & { className?: string }>;

  // Label
  export const Label: ComponentType<HTMLAttributes<HTMLLabelElement> & { htmlFor?: string }>;

  // Select
  export const Select: ComponentType<{
    value?: string;
    defaultValue?: string;
    onValueChange?: (value: string) => void;
    children?: ReactNode;
  }>;
  export const SelectTrigger: ComponentType<{ className?: string; children?: ReactNode }>;
  export const SelectValue: ComponentType<{ placeholder?: string }>;
  export const SelectContent: ComponentType<{ children?: ReactNode }>;
  export const SelectItem: ComponentType<{ value: string; children?: ReactNode }>;

  // Switch
  export const Switch: ComponentType<{
    checked?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    className?: string;
  }>;

  // ScrollArea
  export const ScrollArea: ComponentType<{ className?: string; children?: ReactNode }>;
  export const ScrollBar: ComponentType<{ orientation?: "vertical" | "horizontal" }>;

  // Dialog
  export const Dialog: ComponentType<{ open?: boolean; onOpenChange?: (open: boolean) => void; children?: ReactNode }>;
  export const DialogContent: ComponentType<{ className?: string; children?: ReactNode }>;
  export const DialogDescription: ComponentType<{ className?: string; children?: ReactNode }>;
  export const DialogFooter: ComponentType<{ className?: string; children?: ReactNode }>;
  export const DialogHeader: ComponentType<{ className?: string; children?: ReactNode }>;
  export const DialogTitle: ComponentType<{ className?: string; children?: ReactNode }>;
  export const DialogTrigger: ComponentType<{ asChild?: boolean; children?: ReactNode }>;

  // ContextMenu
  export const ContextMenu: ComponentType<{ children?: ReactNode }>;
  export const ContextMenuContent: ComponentType<{ className?: string; children?: ReactNode }>;
  export const ContextMenuItem: ComponentType<{
    className?: string;
    onSelect?: () => void;
    disabled?: boolean;
    children?: ReactNode;
  }>;
  export const ContextMenuSeparator: ComponentType<Record<string, never>>;
  export const ContextMenuTrigger: ComponentType<{ asChild?: boolean; className?: string; children?: ReactNode }>;

  // DropdownMenu
  export const DropdownMenu: ComponentType<{ children?: ReactNode }>;
  export const DropdownMenuContent: ComponentType<{ align?: string; className?: string; children?: ReactNode }>;
  export const DropdownMenuItem: ComponentType<{
    className?: string;
    onSelect?: () => void;
    disabled?: boolean;
    children?: ReactNode;
  }>;
  export const DropdownMenuSeparator: ComponentType<Record<string, never>>;
  export const DropdownMenuTrigger: ComponentType<{ asChild?: boolean; children?: ReactNode }>;

  // Tooltip
  export const Tooltip: ComponentType<{ children?: ReactNode }>;
  export const TooltipContent: ComponentType<{ children?: ReactNode; side?: string }>;
  export const TooltipProvider: ComponentType<{ children?: ReactNode; delayDuration?: number }>;
  export const TooltipTrigger: ComponentType<{ asChild?: boolean; children?: ReactNode }>;

  // Separator
  export const Separator: ComponentType<{ orientation?: "horizontal" | "vertical"; className?: string }>;

  // ConfirmDialog
  export const ConfirmDialog: ComponentType<{
    open: boolean;
    onOpenChange: (open: boolean) => void;
    title: string;
    description?: string;
    confirmText?: string;
    cancelText?: string;
    onConfirm: () => void;
    variant?: "default" | "destructive";
  }>;

  // useResizeHandle
  export function useResizeHandle(options: {
    defaultWidth: number;
    minWidth: number;
    maxWidth: number;
    reverse?: boolean;
    storageKey?: string;
  }): { width: number; isResizing: boolean; handleMouseDown: (e: React.MouseEvent) => void };

  // Textarea
  export const Textarea: ComponentType<InputHTMLAttributes<HTMLTextAreaElement> & { className?: string }>;

  // Popover
  export const Popover: ComponentType<{ open?: boolean; onOpenChange?: (open: boolean) => void; children?: ReactNode }>;
  export const PopoverContent: ComponentType<{ className?: string; align?: string; children?: ReactNode }>;
  export const PopoverTrigger: ComponentType<{ asChild?: boolean; children?: ReactNode }>;

  // Tabs
  export const Tabs: ComponentType<{
    value?: string;
    defaultValue?: string;
    onValueChange?: (value: string) => void;
    className?: string;
    children?: ReactNode;
  }>;
  export const TabsContent: ComponentType<{ value: string; className?: string; children?: ReactNode }>;
  export const TabsList: ComponentType<{ className?: string; children?: ReactNode }>;
  export const TabsTrigger: ComponentType<{ value: string; className?: string; children?: ReactNode }>;

  // Card
  export const Card: ComponentType<{ className?: string; children?: ReactNode }>;
  export const CardContent: ComponentType<{ className?: string; children?: ReactNode }>;
  export const CardHeader: ComponentType<{ className?: string; children?: ReactNode }>;
  export const CardTitle: ComponentType<{ className?: string; children?: ReactNode }>;
}
