// The page's only way out: the extension's own tools (and, for page state, its
// actions), run by the host against the page's asset. A page call is the user's own action, so the host runs it directly
// (no policy check, approval or audit); a tool error rejects the promise.

import { parseSavedConsoles, type SavedConsole } from "./es/tabs";

export const EXT_NAME = "elasticsearch";

export function callTool<T>(tool: string, args: unknown, assetId: number, signal?: AbortSignal): Promise<T> {
  return window.__OPSKAT_EXT__.api.callTool(EXT_NAME, tool, args, assetId, { signal }) as Promise<T>;
}

/** The request tool: any REST call; a 4xx / 5xx answer is a result, not a rejection. */
export function esRequest(
  assetId: number,
  args: { method: string; path: string; body?: string },
  signal?: AbortSignal
): Promise<{ status: number; body: unknown }> {
  return callTool("request", args, assetId, signal);
}

/**
 * The extension's own actions (main.go RegisterAction): page state, not cluster
 * calls. The host scopes each to the page's asset, which is what they are keyed by.
 */
function executeAction(action: string, args: unknown, assetId: number): Promise<unknown> {
  return window.__OPSKAT_EXT__.api.executeAction(EXT_NAME, action, args, undefined, assetId);
}

/** The asset's saved console tabs (console.go). */
export async function loadConsoles(assetId: number): Promise<SavedConsole[]> {
  return parseSavedConsoles(await executeAction("console.load", {}, assetId));
}

/** Replaces the asset's saved console tabs. */
export async function saveConsoles(assetId: number, consoles: SavedConsole[]): Promise<void> {
  await executeAction("console.save", { consoles }, assetId);
}
