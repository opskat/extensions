/** Elasticsearch's own account of a failed request: its error type (may be empty) and reason. */
export interface EsError {
  type: string;
  reason: string;
}

/** Reads a 4xx / 5xx body: `{error: {type, reason}}`, `{error: "…"}`, or anything else as text. */
export function esError(body: unknown): EsError {
  const err = (body as { error?: unknown } | null)?.error;
  if (typeof err === "string") return { type: "", reason: err };
  if (err && typeof err === "object") {
    const e = err as { type?: string; reason?: string };
    return { type: e.type ?? "", reason: e.reason ?? JSON.stringify(err) };
  }
  return { type: "", reason: typeof body === "string" ? body : JSON.stringify(body) };
}

/** The message of a failed tool call (the host rejects with the tool's error text). */
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export type LoadErrorKind = "auth" | "unreachable" | "other";

/**
 * Classifies why the cluster could not be loaded, from the error the health tool
 * returns (tools.go: "cannot reach Elasticsearch: …" when no answer came back,
 * "Elasticsearch returned HTTP <status>: …" when ES refused).
 */
export function loadErrorKind(message: string): LoadErrorKind {
  if (/returned HTTP 40[13]\b/.test(message)) return "auth";
  if (message.includes("cannot reach Elasticsearch")) return "unreachable";
  return "other";
}
