import { describe, expect, it } from "vitest";
import { esError, loadErrorKind } from "./errors";

describe("esError", () => {
  it("takes type and reason from an ES error body", () => {
    expect(esError({ error: { type: "index_not_found_exception", reason: "no such index [x]" }, status: 404 })).toEqual({
      type: "index_not_found_exception",
      reason: "no such index [x]",
    });
  });

  it("keeps a plain-string error and a text body as the reason", () => {
    expect(esError({ error: "Incorrect HTTP method" })).toEqual({ type: "", reason: "Incorrect HTTP method" });
    expect(esError("502 Bad Gateway")).toEqual({ type: "", reason: "502 Bad Gateway" });
  });

  it("shows any other body as JSON text", () => {
    expect(esError({ acknowledged: false })).toEqual({ type: "", reason: '{"acknowledged":false}' });
  });
});

describe("loadErrorKind", () => {
  it("tells an authentication failure from an unreachable cluster", () => {
    expect(loadErrorKind("Elasticsearch returned HTTP 401: security_exception: unable to authenticate user")).toBe("auth");
    expect(loadErrorKind("Elasticsearch returned HTTP 403: security_exception: action is unauthorized")).toBe("auth");
    expect(loadErrorKind('cannot reach Elasticsearch: Post "https://es:9200/": dial tcp: i/o timeout')).toBe("unreachable");
    expect(loadErrorKind("Elasticsearch returned HTTP 500: boom")).toBe("other");
  });

  it("treats any failure that carries no ES status as unreachable, not as an ES error", () => {
    expect(loadErrorKind("context deadline exceeded")).toBe("unreachable");
    expect(loadErrorKind("tool call timed out after 30s")).toBe("unreachable");
    expect(loadErrorKind("dial tcp 10.0.0.1:9200: connect: connection refused")).toBe("unreachable");
    expect(loadErrorKind("tls: failed to verify certificate")).toBe("unreachable");
  });
});
