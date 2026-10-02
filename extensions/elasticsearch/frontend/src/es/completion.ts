// Keyword-level completion for the console and the documents tab's DSL box:
// what the cursor is in (context detection), then what to offer there. Pure —
// the Monaco glue (components/console/monaco.ts) only converts offsets and items.
// No per-API body schema: every body gets the same DSL keys and field names.

import { isCommentLine, METHODS, parseRequestLine } from "./console";

/** APIs offered for the first path segment. */
const ROOT_APIS = [
  "_search",
  "_count",
  "_bulk",
  "_msearch",
  "_mget",
  "_mapping",
  "_settings",
  "_aliases",
  "_alias",
  "_stats",
  "_reindex",
  "_delete_by_query",
  "_update_by_query",
  "_cat/indices",
  "_cat/nodes",
  "_cat/health",
  "_cat/shards",
  "_cat/aliases",
  "_cat/allocation",
  "_cluster/health",
  "_cluster/settings",
  "_cluster/stats",
  "_cluster/allocation/explain",
  "_nodes/stats",
  "_tasks",
];

/** APIs offered after an index: `/<index>/…`. */
const INDEX_APIS = [
  "_search",
  "_count",
  "_doc",
  "_create",
  "_update",
  "_bulk",
  "_msearch",
  "_mget",
  "_mapping",
  "_settings",
  "_alias",
  "_stats",
  "_refresh",
  "_flush",
  "_forcemerge",
  "_delete_by_query",
  "_update_by_query",
  "_open",
  "_close",
];

const QUERY_KEYS = [
  "query",
  "bool",
  "must",
  "filter",
  "should",
  "must_not",
  "minimum_should_match",
  "match",
  "match_all",
  "match_phrase",
  "multi_match",
  "term",
  "terms",
  "range",
  "gt",
  "gte",
  "lt",
  "lte",
  "exists",
  "wildcard",
  "prefix",
  "ids",
  "query_string",
  "nested",
  "path",
  "field",
  "fields",
];
const REQUEST_KEYS = ["aggs", "sort", "order", "size", "from", "_source", "track_total_hits", "highlight", "search_after"];
const AGGREGATION_TYPES = [
  "terms",
  "avg",
  "sum",
  "min",
  "max",
  "cardinality",
  "value_count",
  "stats",
  "percentiles",
  "date_histogram",
  "calendar_interval",
  "histogram",
  "interval",
  "range",
  "filters",
  "top_hits",
];

/** Every DSL key offered in a body, in the order offered. */
export const DSL_KEYS = [...new Set([...QUERY_KEYS, ...REQUEST_KEYS, ...AGGREGATION_TYPES])];

/** What the page has loaded for completion; anything missing is just not offered. */
export interface CompletionData {
  indices: readonly string[];
  aliases: readonly string[];
  /** The mapping's fields of a request's target (index, alias, pattern or list) as dotted paths. */
  fields(target: string): readonly string[];
}

/**
 * Where the cursor is. `from` is the offset the completion replaces from; it
 * replaces up to the cursor, and what lies between is the typed prefix.
 */
export type CompletionContext =
  /** space: nothing follows on the line, so the method goes in with the space before its path. */
  | { kind: "method"; from: number; space: boolean }
  /** root: the first path segment; index: the segment after `/<index>/`. slash: the path's `/` is still to be typed. */
  | { kind: "path"; from: number; scope: "root" | "index"; slash: boolean }
  /** target: the request's index part, whose mapping gives the field names. */
  | { kind: "body"; from: number; position: "key" | "value"; quoted: boolean; target: string | null };

export type SuggestionKind = "method" | "api" | "index" | "alias" | "key" | "field";

export interface Suggestion {
  label: string;
  insertText: string;
  kind: SuggestionKind;
}

const METHOD_PREFIX = /^\s*[A-Za-z]*$/;
const PATH_PREFIX = /^\s*([A-Za-z]+)\s+(\S*)$/;

/** The completion context at `offset` in a console's text, or null where nothing is offered. */
export function consoleContext(text: string, offset: number): CompletionContext | null {
  const lineStart = text.lastIndexOf("\n", offset - 1) + 1;
  const lineEnd = text.indexOf("\n", offset);
  const line = text.slice(lineStart, lineEnd < 0 ? text.length : lineEnd);
  if (isCommentLine(line)) return null;
  const before = text.slice(lineStart, offset);

  const path = PATH_PREFIX.exec(before);
  if (path && (METHODS as readonly string[]).includes(path[1].toUpperCase())) return pathContext(path[2], offset);
  const method = (): CompletionContext | null =>
    METHOD_PREFIX.test(before)
      ? { kind: "method", from: offset - before.trimStart().length, space: !line.slice(before.length).trim() }
      : null;
  if (parseRequestLine(line)) return method();

  // Below a request line: its body, unless every JSON value in it is closed —
  // then the cursor is where a new request (or NDJSON line) may start.
  const requestLine = findRequestLineAbove(text, lineStart);
  if (requestLine) {
    const body = blankComments(text.slice(requestLine.bodyStart, offset));
    const ctx = jsonContext(body, requestLine.bodyStart, targetOf(requestLine.path));
    if (ctx !== "top") return ctx;
  }
  return method();
}

/** The completion context at `offset` in a body-only editor (the DSL box) searching `target`. */
export function bodyContext(text: string, offset: number, target: string | null): CompletionContext | null {
  const ctx = jsonContext(text.slice(0, offset), 0, target);
  return ctx === "top" ? null : ctx;
}

/** What to offer in a context. */
export function suggest(ctx: CompletionContext, data: CompletionData): Suggestion[] {
  switch (ctx.kind) {
    case "method":
      return METHODS.map((m) => ({ label: m, insertText: ctx.space ? `${m} ` : m, kind: "method" }));
    case "path": {
      const lead = ctx.slash ? "/" : "";
      const item = (label: string, kind: SuggestionKind): Suggestion => ({ label, insertText: lead + label, kind });
      if (ctx.scope === "index") return INDEX_APIS.map((a) => item(a, "api"));
      return [
        ...ROOT_APIS.map((a) => item(a, "api")),
        ...data.indices.map((i) => item(i, "index")),
        ...data.aliases.map((a) => item(a, "alias")),
      ];
    }
    case "body": {
      const item = (label: string, kind: SuggestionKind): Suggestion => ({
        label,
        insertText: ctx.quoted ? label : JSON.stringify(label),
        kind,
      });
      const fields = ctx.target ? data.fields(ctx.target).map((f) => item(f, "field")) : [];
      return ctx.position === "key" ? [...DSL_KEYS.map((k) => item(k, "key")), ...fields] : fields;
    }
  }
}

/** The index part of a request path — its first segment unless that is an API (`_…`). */
function targetOf(path: string): string | null {
  const first = path.split("?")[0].replace(/^\//, "").split("/")[0];
  return first && !first.startsWith("_") ? first : null;
}

function pathContext(typed: string, offset: number): CompletionContext | null {
  if (typed.includes("?")) return null;
  const slashLen = typed.startsWith("/") ? 1 : 0;
  const rest = typed.slice(slashLen);
  if (rest.startsWith("_") || !rest.includes("/")) {
    return { kind: "path", from: offset - rest.length, scope: "root", slash: typed === "" };
  }
  const segs = rest.split("/");
  if (segs.length !== 2) return null;
  return { kind: "path", from: offset - segs[1].length, scope: "index", slash: false };
}

function findRequestLineAbove(text: string, lineStart: number): { path: string; bodyStart: number } | null {
  let end = lineStart - 1; // the "\n" ending the line above
  while (end >= 0) {
    const start = text.lastIndexOf("\n", end - 1) + 1;
    const req = parseRequestLine(text.slice(start, end));
    if (req) return { path: req.path, bodyStart: end + 1 };
    end = start - 1;
  }
  return null;
}

/** Comment lines as spaces: skipped by the JSON scan, offsets unchanged. */
function blankComments(body: string): string {
  return body
    .split("\n")
    .map((l) => (isCommentLine(l) ? " ".repeat(l.length) : l))
    .join("\n");
}

type Expect = "key" | "colon" | "value" | "comma";
interface Container {
  type: "{" | "[";
  expect: Expect;
}

const WORD_CHAR = /[A-Za-z0-9_.+-]/;

/**
 * Scans partial JSON (or NDJSON) up to the cursor at its end and tells where the
 * cursor is: "top" when no value is open, else the body context (null where
 * nothing is offered). `base` is the scanned text's offset in the document.
 */
function jsonContext(json: string, base: number, target: string | null): CompletionContext | null | "top" {
  const stack: Container[] = [];
  let str: { start: number; key: boolean } | null = null;
  let word: number | null = null;

  for (let i = 0; i < json.length; i++) {
    const ch = json[i];
    const top = stack[stack.length - 1] as Container | undefined;
    if (str) {
      if (ch === "\\") i++;
      else if (ch === '"') {
        if (top) top.expect = str.key ? "colon" : "comma";
        str = null;
      }
      continue;
    }
    if (WORD_CHAR.test(ch)) {
      word ??= i;
      continue;
    }
    if (word !== null) {
      // A bare word ends: a literal value (true, 12), or an unquoted key.
      if (top) top.expect = top.expect === "key" ? "colon" : "comma";
      word = null;
    }
    switch (ch) {
      case '"':
        str = { start: i, key: top?.type === "{" && top.expect === "key" };
        break;
      case "{":
      case "[":
        if (top) top.expect = "comma";
        stack.push({ type: ch, expect: ch === "{" ? "key" : "value" });
        break;
      case "}":
      case "]":
        stack.pop();
        break;
      case ":":
        if (top) top.expect = "value";
        break;
      case ",":
        if (top) top.expect = top.type === "{" ? "key" : "value";
        break;
    }
  }

  const top = stack[stack.length - 1] as Container | undefined;
  if (!top) return "top";
  if (str) return { kind: "body", from: base + str.start + 1, position: str.key ? "key" : "value", quoted: true, target };
  if (top.type === "{" && top.expect === "key") {
    return { kind: "body", from: base + (word ?? json.length), position: "key", quoted: false, target };
  }
  return null;
}

/** Every field of a `mapping` result (`{index: {mappings: {properties}}}`) as a dotted path, sorted. */
export function mappingFields(mapping: unknown): string[] {
  const out = new Set<string>();
  if (mapping && typeof mapping === "object") {
    for (const index of Object.values(mapping)) {
      addProperties((index as { mappings?: { properties?: unknown } } | null)?.mappings?.properties, "", out);
    }
  }
  return [...out].sort();
}

interface FieldDef {
  properties?: unknown;
  fields?: unknown;
}

function addProperties(properties: unknown, prefix: string, out: Set<string>): void {
  if (!properties || typeof properties !== "object") return;
  for (const [name, def] of Object.entries(properties as Record<string, FieldDef>)) {
    const path = prefix + name;
    out.add(path);
    addProperties(def?.properties, `${path}.`, out);
    // Multi-fields: title.keyword and the like.
    if (def?.fields && typeof def.fields === "object") {
      for (const sub of Object.keys(def.fields)) out.add(`${path}.${sub}`);
    }
  }
}
