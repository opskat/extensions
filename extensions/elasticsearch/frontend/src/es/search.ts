import { esError } from "./errors";
import type { RequestResult, SearchHit } from "./types";

// The documents view pages with from / size, which the search tool does not
// take, so it sends its _search through the request tool. The query string and
// sort go in the URL (their syntax is the same one the user types), from / size
// and the DSL query in the body.

export type QueryMode = "q" | "dsl";

export interface SearchInput {
  index: string;
  mode: QueryMode;
  /** Lucene query string (mode "q"); empty matches everything. */
  q: string;
  /** Query DSL — the value of "query" (mode "dsl"); empty matches everything. */
  dsl: string;
  /** `field:asc|desc`, comma-separated for several. */
  sort: string;
  from: number;
  size: number;
}

export interface RequestArgs {
  method: string;
  path: string;
  body: string;
}

export type SearchRequest = { ok: true; args: RequestArgs } | { ok: false; reason: string };

export function buildSearchRequest(input: SearchInput): SearchRequest {
  const params = new URLSearchParams();
  const body: Record<string, unknown> = { from: input.from, size: input.size, track_total_hits: true };
  if (input.mode === "q") {
    if (input.q.trim()) params.set("q", input.q.trim());
  } else if (input.dsl.trim()) {
    let query: unknown;
    try {
      query = JSON.parse(input.dsl);
    } catch (err) {
      return { ok: false, reason: (err as Error).message };
    }
    if (query === null || typeof query !== "object" || Array.isArray(query)) {
      return { ok: false, reason: "the query must be a JSON object" };
    }
    body.query = query;
  }
  if (input.sort.trim()) params.set("sort", input.sort.trim());
  const qs = params.toString();
  return {
    ok: true,
    args: {
      method: "POST",
      path: `/${encodeURIComponent(input.index)}/_search${qs ? `?${qs}` : ""}`,
      body: JSON.stringify(body),
    },
  };
}

export type SearchResponse =
  | { ok: true; total: number; totalRelation: string; took: number; hits: SearchHit[] }
  | { ok: false; status: number; type: string; reason: string };

interface RawSearch {
  took: number;
  hits: { total: { value: number; relation: string }; hits: SearchHit[] };
}

export function parseSearchResponse({ status, body }: RequestResult): SearchResponse {
  if (status < 200 || status >= 300) return { ok: false, status, ...esError(body) };
  const r = body as RawSearch;
  return { ok: true, total: r.hits.total.value, totalRelation: r.hits.total.relation, took: r.took, hits: r.hits.hits };
}
