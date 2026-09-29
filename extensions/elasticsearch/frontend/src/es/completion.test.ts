import { describe, expect, it } from "vitest";
import {
  bodyContext,
  consoleContext,
  DSL_KEYS,
  mappingFields,
  suggest,
  type CompletionContext,
  type CompletionData,
} from "./completion";

/** Splits a document written with `|` at the cursor into its text and the cursor's offset. */
function cursor(doc: string): { text: string; offset: number } {
  const offset = doc.indexOf("|");
  return { text: doc.slice(0, offset) + doc.slice(offset + 1), offset };
}

const data: CompletionData = {
  indices: ["logs-app", "orders", ".kibana_1"],
  aliases: ["logs"],
  fields: (target) => (target === "orders" ? ["sku", "customer", "customer.name", "customer.name.keyword"] : []),
};

const noData: CompletionData = { indices: [], aliases: [], fields: () => [] };

function consoleAt(doc: string): { ctx: CompletionContext | null; typed: string } {
  const { text, offset } = cursor(doc);
  const ctx = consoleContext(text, offset);
  return { ctx, typed: ctx ? text.slice(ctx.from, offset) : "" };
}

const labels = (ctx: CompletionContext | null, d: CompletionData = data) => (ctx ? suggest(ctx, d).map((s) => s.label) : []);
const inserts = (ctx: CompletionContext | null, d: CompletionData = data) =>
  ctx ? suggest(ctx, d).map((s) => s.insertText) : [];

describe("consoleContext + suggest: line start", () => {
  it("offers the methods at the start of an empty document and a new line", () => {
    for (const doc of ["|", "G|", "GET /a\n\nPO|"]) {
      const { ctx, typed } = consoleAt(doc);
      expect(ctx?.kind).toBe("method");
      expect(labels(ctx)).toEqual(["GET", "POST", "PUT", "DELETE", "HEAD"]);
      expect(typed).toBe(doc.slice(doc.lastIndexOf("\n") + 1, doc.indexOf("|")));
    }
  });

  it("inserts the method with the space before its path, unless the line goes on after the cursor", () => {
    expect(inserts(consoleAt("PO|").ctx)).toContain("POST ");
    const { ctx, typed } = consoleAt("GE| /orders");
    expect(typed).toBe("GE");
    expect(inserts(ctx)).toContain("GET");
    expect(inserts(ctx)).not.toContain("GET ");
  });

  it("offers no methods on a line inside an open body", () => {
    const { ctx } = consoleAt('GET /orders/_search\n{\n  q|\n}');
    expect(ctx?.kind).toBe("body");
  });

  it("offers nothing on a comment line", () => {
    expect(consoleAt("# G|").ctx).toBeNull();
    expect(consoleAt('GET /a\n{\n  # "qu|\n}').ctx).toBeNull();
  });
});

describe("consoleContext + suggest: path", () => {
  it("offers the common APIs and the cluster's indices and aliases for the first segment", () => {
    const { ctx, typed } = consoleAt("GET /|");
    expect(ctx?.kind).toBe("path");
    expect(typed).toBe("");
    const got = labels(ctx);
    for (const api of [
      "_search",
      "_count",
      "_bulk",
      "_mapping",
      "_settings",
      "_cat/indices",
      "_cat/nodes",
      "_cluster/health",
      "_aliases",
    ]) {
      expect(got).toContain(api);
    }
    expect(got).toEqual(expect.arrayContaining(["logs-app", "orders", ".kibana_1", "logs"]));
    expect(suggest(ctx!, data).find((s) => s.label === "logs")?.kind).toBe("alias");
    expect(suggest(ctx!, data).find((s) => s.label === "orders")?.kind).toBe("index");
  });

  it("replaces what was typed of the segment, a multi-segment API included", () => {
    expect(consoleAt("GET /ord|").typed).toBe("ord");
    expect(consoleAt("GET /_cat/ind|").typed).toBe("_cat/ind");
  });

  it("puts the leading slash in when nothing of the path was typed yet", () => {
    const { ctx } = consoleAt("GET |");
    expect(ctx?.kind).toBe("path");
    expect(inserts(ctx)).toContain("/_search");
    expect(inserts(ctx)).toContain("/orders");
  });

  it("offers the index-level APIs after an index, not index names", () => {
    const { ctx, typed } = consoleAt("POST /orders/_d|");
    expect(typed).toBe("_d");
    const got = labels(ctx);
    expect(got).toEqual(expect.arrayContaining(["_search", "_count", "_doc", "_bulk", "_mapping", "_settings"]));
    expect(got).not.toContain("orders");
    expect(got).not.toContain("_cat/indices");
  });

  it("offers nothing in the query string or past the second segment", () => {
    expect(consoleAt("GET /orders/_search?si|").ctx).toBeNull();
    expect(consoleAt("GET /orders/_doc/1|").ctx).toBeNull();
  });

  it("still offers the APIs when the cluster's indices and aliases are not loaded", () => {
    const { ctx } = consoleAt("GET /|");
    expect(labels(ctx, noData)).toContain("_search");
    expect(labels(ctx, noData)).not.toContain("orders");
  });
});

describe("consoleContext + suggest: body", () => {
  it("offers the DSL keys and the target index's fields at a key inside quotes", () => {
    const { ctx, typed } = consoleAt('GET /orders/_search\n{\n  "que|"\n}');
    expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: true, target: "orders" });
    expect(typed).toBe("que");
    const got = labels(ctx);
    for (const key of [
      "query",
      "bool",
      "must",
      "filter",
      "should",
      "must_not",
      "match",
      "match_phrase",
      "term",
      "terms",
      "range",
      "exists",
      "wildcard",
      "query_string",
      "aggs",
      "sort",
      "size",
      "from",
      "_source",
      "track_total_hits",
      "highlight",
      "avg",
      "date_histogram",
    ]) {
      expect(got).toContain(key);
    }
    expect(got).toEqual(expect.arrayContaining(["sku", "customer.name", "customer.name.keyword"]));
    expect(suggest(ctx!, data).find((s) => s.label === "customer.name")?.kind).toBe("field");
  });

  it("replaces a dotted field path typed so far", () => {
    const { ctx, typed } = consoleAt('GET /orders/_search\n{ "query": { "term": { "customer.na|" } } }');
    expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: true });
    expect(typed).toBe("customer.na");
  });

  it("quotes the key when the cursor is at a key without a quote", () => {
    for (const doc of ['GET /orders/_search\n{\n  |\n}', 'GET /orders/_search\n{ "size": 1, qu|']) {
      const { ctx } = consoleAt(doc);
      expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: false });
      expect(inserts(ctx)).toContain('"query"');
    }
  });

  it("offers only field names in a string value", () => {
    const { ctx, typed } = consoleAt('GET /orders/_search\n{ "sort": ["sku"], "aggs": { "a": { "terms": { "field": "cust|" } } } }');
    expect(ctx).toMatchObject({ kind: "body", position: "value" });
    expect(typed).toBe("cust");
    expect(labels(ctx)).toEqual(["sku", "customer", "customer.name", "customer.name.keyword"]);
  });

  it("offers nothing at a non-string value", () => {
    expect(consoleAt('GET /orders/_search\n{ "size": 1|').ctx).toBeNull();
    expect(consoleAt('GET /orders/_search\n{ "size": |').ctx).toBeNull();
  });

  it("reads each NDJSON line on its own", () => {
    const { ctx } = consoleAt('POST /orders/_bulk\n{ "index": {} }\n{ "sk|');
    expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: true, target: "orders" });
  });

  it("uses the first segment of the request line as the target, and none for a cluster API", () => {
    expect(consoleAt('GET /logs-*,orders/_search?size=5\n{ "|').ctx).toMatchObject({ target: "logs-*,orders" });
    expect(consoleAt('GET /_search\n{ "|').ctx).toMatchObject({ target: null });
  });

  it("offers the DSL keys alone when the target's mapping is not loaded, or there is no target", () => {
    const { ctx } = consoleAt('GET /_search\n{ "|');
    expect(labels(ctx)).toEqual(DSL_KEYS);
    expect(labels(consoleAt('GET /orders/_search\n{ "|').ctx, noData)).toEqual(DSL_KEYS);
  });

  it("skips a comment line inside the body", () => {
    const { ctx } = consoleAt('GET /orders/_search\n{\n  # a { note "\n  "qu|');
    expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: true });
  });
});

describe("bodyContext (the documents tab's DSL box)", () => {
  it("gives the same body suggestions for its own index", () => {
    const { text, offset } = cursor('{ "query": { "match": { "cu|" } } }');
    const ctx = bodyContext(text, offset, "orders");
    expect(ctx).toMatchObject({ kind: "body", position: "key", quoted: true, target: "orders" });
    expect(labels(ctx)).toEqual(expect.arrayContaining(["match_phrase", "customer.name"]));
  });

  it("offers no methods at the start of the box", () => {
    const { text, offset } = cursor("|");
    expect(bodyContext(text, offset, "orders")).toBeNull();
  });
});

describe("mappingFields", () => {
  it("lists every field as a dotted path, objects, nested fields and multi-fields included, across indices", () => {
    const mapping = {
      "orders-1": {
        mappings: {
          properties: {
            sku: { type: "keyword" },
            customer: {
              properties: {
                name: { type: "text", fields: { keyword: { type: "keyword" } } },
              },
            },
            lines: { type: "nested", properties: { qty: { type: "integer" } } },
          },
        },
      },
      "orders-2": { mappings: { properties: { sku: { type: "keyword" }, note: { type: "text" } } } },
    };
    expect(mappingFields(mapping)).toEqual([
      "customer",
      "customer.name",
      "customer.name.keyword",
      "lines",
      "lines.qty",
      "note",
      "sku",
    ]);
  });

  it("gives nothing for an index without mappings or a body of another shape", () => {
    expect(mappingFields({ empty: { mappings: {} } })).toEqual([]);
    expect(mappingFields("oops")).toEqual([]);
    expect(mappingFields(null)).toEqual([]);
  });
});
