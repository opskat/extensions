import { describe, expect, it } from "vitest";
import { requestAt, responseBytes, responseText, splitRequests } from "./console";

const text = (...lines: string[]) => lines.join("\n");

describe("splitRequests", () => {
  it("reads a request line and the JSON body up to the next request line", () => {
    const reqs = splitRequests(
      text(
        "GET /logs-*/_search",
        "{",
        '  "query": { "match_all": {} }',
        "}",
        "",
        "POST /orders/_doc",
        '{ "sku": "A-1" }'
      )
    );
    expect(reqs.map(({ method, path, body }) => ({ method, path, body }))).toEqual([
      { method: "GET", path: "/logs-*/_search", body: '{\n  "query": { "match_all": {} }\n}' },
      { method: "POST", path: "/orders/_doc", body: '{ "sku": "A-1" }' },
    ]);
  });

  it("takes a request without a body, several in a row", () => {
    const reqs = splitRequests(text("GET /_cat/indices?v&s=index", "DELETE /logs-old-1,logs-old-2", "HEAD /orders"));
    expect(reqs.map((r) => [r.method, r.path, r.body])).toEqual([
      ["GET", "/_cat/indices?v&s=index", ""],
      ["DELETE", "/logs-old-1,logs-old-2", ""],
      ["HEAD", "/orders", ""],
    ]);
  });

  it("drops # comment lines, inside a body too", () => {
    const reqs = splitRequests(
      text("# the cluster", "GET /_cluster/health", "# a note", "", "POST /x/_search", "{", "  # not sent", '  "size": 1', "}")
    );
    expect(reqs.map((r) => [r.path, r.body])).toEqual([
      ["/_cluster/health", ""],
      ["/x/_search", '{\n  "size": 1\n}'],
    ]);
  });

  it("keeps a multi-line NDJSON body line by line", () => {
    const [bulk] = splitRequests(
      text(
        "POST /_bulk",
        '{ "index": { "_index": "orders", "_id": "1" } }',
        '{ "sku": "A-1" }',
        '{ "delete": { "_index": "orders", "_id": "2" } }',
        ""
      )
    );
    expect(bulk.body.split("\n")).toEqual([
      '{ "index": { "_index": "orders", "_id": "1" } }',
      '{ "sku": "A-1" }',
      '{ "delete": { "_index": "orders", "_id": "2" } }',
    ]);
  });

  it("accepts a lower-case method and a path without its leading slash", () => {
    const [r] = splitRequests("get _search");
    expect([r.method, r.path]).toEqual(["GET", "/_search"]);
  });

  it("finds nothing in comments and blank lines", () => {
    expect(splitRequests(text("# only notes", "", "   "))).toEqual([]);
  });

  it("does not take a word that merely starts like a method for a request line", () => {
    const reqs = splitRequests(text("GET /a", "GETTER /b"));
    expect(reqs).toHaveLength(1);
    expect(reqs[0].body).toBe("GETTER /b");
  });
});

describe("requestAt", () => {
  const doc = text(
    "# console intro", // 0
    "", // 1
    "GET /logs/_search", // 2
    "{", // 3
    "  # inside the body", // 4
    '  "size": 1', // 5
    "}", // 6
    "", // 7
    "# count the orders", // 8
    "GET /orders/_count", // 9
    "", // 10
    "" // 11
  );
  const reqs = splitRequests(doc);
  const pathAt = (line: number) => requestAt(reqs, line)?.path ?? null;

  it("targets the request whose line the cursor is on", () => {
    expect(pathAt(2)).toBe("/logs/_search");
    expect(pathAt(9)).toBe("/orders/_count");
  });

  it("targets the request whose body the cursor is in, a comment in it included", () => {
    expect(pathAt(3)).toBe("/logs/_search");
    expect(pathAt(4)).toBe("/logs/_search");
    expect(pathAt(6)).toBe("/logs/_search");
  });

  it("gives a comment directly above a request line to that request", () => {
    expect(pathAt(8)).toBe("/orders/_count");
  });

  it("gives the blank lines between two requests to the one above", () => {
    expect(pathAt(7)).toBe("/logs/_search");
    expect(pathAt(11)).toBe("/orders/_count");
  });

  it("targets nothing above the first request", () => {
    expect(pathAt(0)).toBeNull();
    expect(pathAt(1)).toBeNull();
  });
});

describe("responseText", () => {
  it("shows a JSON body pretty-printed and a text body as it came", () => {
    expect(responseText({ acknowledged: true })).toBe('{\n  "acknowledged": true\n}');
    expect(responseText("green open orders\n")).toBe("green open orders\n");
    expect(responseText("")).toBe("");
  });
});

describe("responseBytes", () => {
  it("counts the UTF-8 bytes of the body as sent, compact JSON for a parsed body", () => {
    expect(responseBytes({ a: "é" })).toBe(new TextEncoder().encode('{"a":"é"}').length);
    expect(responseBytes("é\n")).toBe(3);
    expect(responseBytes("")).toBe(0);
  });
});
