// The console's text format: each request is a `METHOD /path` line, and the
// lines after it up to the next request line are its body — JSON, or NDJSON
// (one JSON document per line) for _bulk / _msearch. A line starting with `#` is
// a comment and is never sent. Lines are 0-based throughout.

export const METHODS = ["GET", "POST", "PUT", "DELETE", "HEAD"] as const;

/** A request line: a method, then the path (and query) to the end of the line. */
const REQUEST_LINE = new RegExp(`^\\s*(${METHODS.join("|")})(?:\\s+(.*?))?\\s*$`, "i");
const COMMENT_LINE = /^\s*#/;

export function isCommentLine(line: string): boolean {
  return COMMENT_LINE.test(line);
}

/** The method and path of a request line, or null for any other line. */
export function parseRequestLine(line: string): { method: string; path: string } | null {
  const m = REQUEST_LINE.exec(line);
  if (!m) return null;
  const path = m[2] ?? "";
  // Like Kibana, "GET _search" means "GET /_search".
  return { method: m[1].toUpperCase(), path: path && !path.startsWith("/") ? `/${path}` : path };
}

export interface ConsoleRequest {
  method: string;
  path: string;
  /** The body lines without comments and without leading / trailing blank lines; "" for none. */
  body: string;
  /** The request line. */
  line: number;
  /** The lines the request owns for "run the request under the cursor", inclusive. */
  startLine: number;
  endLine: number;
}

function splitLines(text: string): string[] {
  return text.split(/\r?\n/);
}

export function splitRequests(text: string): ConsoleRequest[] {
  const lines = splitLines(text);
  const starts: number[] = [];
  lines.forEach((l, i) => {
    if (parseRequestLine(l)) starts.push(i);
  });

  return starts.map((line, k) => {
    const next = k + 1 < starts.length ? starts[k + 1] : lines.length;
    const { method, path } = parseRequestLine(lines[line])!;
    const bodyLines = lines.slice(line + 1, next).filter((l) => !isCommentLine(l));
    while (bodyLines.length && !bodyLines[0].trim()) bodyLines.shift();
    while (bodyLines.length && !bodyLines[bodyLines.length - 1].trim()) bodyLines.pop();
    return {
      method,
      path,
      body: bodyLines.join("\n"),
      line,
      startLine: ownStart(lines, line),
      endLine: k + 1 < starts.length ? ownStart(lines, next) - 1 : lines.length - 1,
    };
  });
}

/**
 * Where a request's own lines begin: its request line, or the run of comment
 * lines directly above it — a comment right above a request describes it. Blank
 * lines and the comments before them belong to the request above.
 */
function ownStart(lines: string[], requestLine: number): number {
  let start = requestLine;
  while (start > 0 && isCommentLine(lines[start - 1])) start--;
  return start;
}

/** The request that owns a line, or null above the first request. */
export function requestAt(requests: ConsoleRequest[], line: number): ConsoleRequest | null {
  return requests.find((r) => line >= r.startLine && line <= r.endLine) ?? null;
}

/** A response body as text: JSON pretty-printed, a text body (a _cat table, an empty HEAD answer) as it came. */
export function responseText(body: unknown): string {
  return typeof body === "string" ? body : JSON.stringify(body, null, 2);
}

/** The response's size in bytes, as ES sent it: compact JSON for a parsed body. */
export function responseBytes(body: unknown): number {
  return new TextEncoder().encode(typeof body === "string" ? body : JSON.stringify(body)).length;
}
