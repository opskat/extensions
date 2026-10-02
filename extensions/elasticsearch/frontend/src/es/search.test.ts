import { describe, expect, it } from "vitest";
import { buildSearchRequest, parseSearchResponse } from "./search";

const base = { index: "logs-app", mode: "q" as const, q: "", dsl: "", sort: "", from: 100, size: 50 };

describe("buildSearchRequest", () => {
  it("POSTs to the index's _search with from, size and an exact total", () => {
    const r = buildSearchRequest(base);
    expect(r).toEqual({
      ok: true,
      args: { method: "POST", path: "/logs-app/_search", body: JSON.stringify({ from: 100, size: 50, track_total_hits: true }) },
    });
  });

  it("sends a query string and a sort as URL parameters", () => {
    const r = buildSearchRequest({ ...base, q: " level:error AND status:500 ", sort: "@timestamp:desc" });
    expect(r.ok && r.args.path).toBe("/logs-app/_search?q=level%3Aerror+AND+status%3A500&sort=%40timestamp%3Adesc");
  });

  it("puts the DSL in the body as the query, and ignores the query string in DSL mode", () => {
    const r = buildSearchRequest({ ...base, mode: "dsl", q: "ignored", dsl: '{ "term": { "status": 500 } }' });
    expect(r.ok && r.args.path).toBe("/logs-app/_search");
    expect(r.ok && JSON.parse(r.args.body)).toEqual({ from: 100, size: 50, track_total_hits: true, query: { term: { status: 500 } } });
  });

  it("searches everything for an empty DSL box", () => {
    const r = buildSearchRequest({ ...base, mode: "dsl", dsl: "  " });
    expect(r.ok && JSON.parse(r.args.body)).not.toHaveProperty("query");
  });

  it("refuses DSL that is not a JSON object without sending anything", () => {
    expect(buildSearchRequest({ ...base, mode: "dsl", dsl: "{ term" })).toMatchObject({ ok: false });
    expect(buildSearchRequest({ ...base, mode: "dsl", dsl: "[1]" })).toMatchObject({ ok: false });
  });

  it("escapes the index name in the path", () => {
    const r = buildSearchRequest({ ...base, index: "a+b#c" });
    expect(r.ok && r.args.path).toBe("/a%2Bb%23c/_search");
  });
});

describe("parseSearchResponse", () => {
  it("reads total, relation, took and hits from a 2xx answer", () => {
    const hits = [{ _index: "logs", _id: "1", _source: { a: 1 } }];
    const r = parseSearchResponse({ status: 200, body: { took: 7, hits: { total: { value: 1, relation: "eq" }, hits } } });
    expect(r).toEqual({ ok: true, total: 1, totalRelation: "eq", took: 7, hits });
  });

  it("turns a non-2xx answer into ES's own error", () => {
    const r = parseSearchResponse({
      status: 400,
      body: { error: { type: "query_shard_exception", reason: "Failed to parse query [level:]" }, status: 400 },
    });
    expect(r).toEqual({ ok: false, status: 400, type: "query_shard_exception", reason: "Failed to parse query [level:]" });
  });
});
