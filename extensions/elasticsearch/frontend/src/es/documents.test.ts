import { describe, expect, it } from "vitest";
import { documentColumns, documentRows } from "./documents";
import type { SearchHit } from "./types";

const hit = (id: string, source?: Record<string, unknown>): SearchHit => ({ _index: "logs", _id: id, _source: source });

describe("documentColumns", () => {
  it("is _id followed by the top-level _source keys of the page, in first-seen order", () => {
    const hits = [hit("1", { level: "error", message: "a" }), hit("2", { message: "b", status: 500, level: "warn" })];
    expect(documentColumns(hits)).toEqual(["_id", "level", "message", "status"]);
  });

  it("does not descend into nested objects: an object field is one column", () => {
    const hits = [hit("1", { http: { method: "GET", path: "/" }, tags: ["a"] })];
    expect(documentColumns(hits)).toEqual(["_id", "http", "tags"]);
  });

  it("is only _id for hits without _source and for an empty page", () => {
    expect(documentColumns([hit("1")])).toEqual(["_id"]);
    expect(documentColumns([])).toEqual(["_id"]);
  });
});

describe("documentRows", () => {
  it("puts _id and every top-level _source value in a row, one row per hit", () => {
    const rows = documentRows([hit("1", { level: "error", status: 500, ok: false, gone: null }), hit("2", {})]);
    expect(rows).toEqual([{ _id: "1", level: "error", status: 500, ok: false, gone: null }, { _id: "2" }]);
  });

  it("shows nested objects and arrays as JSON text", () => {
    const [row] = documentRows([hit("1", { http: { method: "GET", latency: 12 }, tags: ["a", "b"], empty: {} })]);
    expect(row.http).toBe('{"method":"GET","latency":12}');
    expect(row.tags).toBe('["a","b"]');
    expect(row.empty).toBe("{}");
  });

  it("leaves a field a hit does not have absent, so the cell is empty", () => {
    const rows = documentRows([hit("1", { a: 1 }), hit("2", { b: 2 })]);
    expect(rows[1]).not.toHaveProperty("a");
  });
});
