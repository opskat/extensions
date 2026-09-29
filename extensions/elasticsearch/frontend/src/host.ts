// The page's only way out: the extension's own tools, run by the host against the
// page's asset. A page call is the user's own action, so the host runs it directly
// (no policy check, approval or audit); a tool error rejects the promise.

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
