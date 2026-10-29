// The page's only way out: the extension's own tools (including the page-only
// console.load / console.save), run by the host against the page's asset. A page
// call is the user's own action, so the host runs it directly (no policy check,
// approval or audit); a tool error rejects the promise.

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
 * Each asset's last queued console.load / console.save (page-only tools, keyed by
 * the page's asset), settled either way.
 */
const consoleCalls = new Map<number, Promise<void>>();

/**
 * Runs an asset's console calls one after another, in the order the page made
 * them. The host runs calls concurrently, so otherwise a page reopened right after
 * closing could load before its closing save is stored, and two saves could land
 * out of order — either way the older text would win.
 */
function inConsoleOrder<T>(assetId: number, call: () => Promise<T>): Promise<T> {
  const result = (consoleCalls.get(assetId) ?? Promise.resolve()).then(call);
  // The next call waits for this one to finish, not to succeed; its error still reaches its caller.
  const settled = result.then(
    () => undefined,
    () => undefined
  );
  consoleCalls.set(assetId, settled);
  void settled.then(() => {
    if (consoleCalls.get(assetId) === settled) consoleCalls.delete(assetId);
  });
  return result;
}

/** The asset's saved console tabs (console.go). */
export function loadConsoles(assetId: number): Promise<SavedConsole[]> {
  return inConsoleOrder(assetId, async () => parseSavedConsoles(await callTool("console.load", {}, assetId)));
}

/** Replaces the asset's saved console tabs. */
export function saveConsoles(assetId: number, consoles: SavedConsole[]): Promise<void> {
  return inConsoleOrder(assetId, async () => {
    // The tool takes the tabs as JSON text: a tool parameter is never a nested object (console.go).
    await callTool("console.save", { consoles: JSON.stringify(consoles) }, assetId);
  });
}
