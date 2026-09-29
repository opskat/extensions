import { describe, expect, it, vi } from "vitest";
import { createCompletionSource, parseAliases } from "./completionSource";

const mapping = { orders: { mappings: { properties: { sku: { type: "keyword" } } } } };

describe("createCompletionSource", () => {
  it("serves the page's loaded indices and nothing else until asked to load", () => {
    const loadAliases = vi.fn(async () => ["logs"]);
    const loadMapping = vi.fn(async () => mapping);
    const src = createCompletionSource({ indices: ["orders", "logs-1"], loadAliases, loadMapping });
    expect(src.data.indices).toEqual(["orders", "logs-1"]);
    expect(src.data.aliases).toEqual([]);
    expect(src.data.fields("orders")).toEqual([]);
    expect(loadAliases).not.toHaveBeenCalled();
    expect(loadMapping).not.toHaveBeenCalled();
  });

  it("loads aliases and a target's fields on demand, once each", async () => {
    const loadAliases = vi.fn(async () => ["logs"]);
    const loadMapping = vi.fn(async () => mapping);
    const src = createCompletionSource({ indices: [], loadAliases, loadMapping });
    await Promise.all([src.ensure({ aliases: true, target: "orders" }), src.ensure({ aliases: true, target: "orders" })]);
    await src.ensure({ aliases: true, target: "orders" });
    expect(src.data.aliases).toEqual(["logs"]);
    expect(src.data.fields("orders")).toEqual(["sku"]);
    expect(loadAliases).toHaveBeenCalledTimes(1);
    expect(loadMapping).toHaveBeenCalledTimes(1);
    expect(loadMapping).toHaveBeenCalledWith("orders");
  });

  it("treats a failed load as no suggestions, without rejecting or retrying", async () => {
    const loadAliases = vi.fn(async () => {
      throw new Error("HTTP 403");
    });
    const loadMapping = vi.fn(async () => {
      throw new Error("index_not_found_exception");
    });
    const src = createCompletionSource({ indices: ["orders"], loadAliases, loadMapping });
    await expect(src.ensure({ aliases: true, target: "nope" })).resolves.toBeUndefined();
    await src.ensure({ aliases: true, target: "nope" });
    expect(src.data.aliases).toEqual([]);
    expect(src.data.fields("nope")).toEqual([]);
    expect(loadAliases).toHaveBeenCalledTimes(1);
    expect(loadMapping).toHaveBeenCalledTimes(1);
  });
});

describe("parseAliases", () => {
  it("lists each alias of a _cat/aliases?format=json body once", () => {
    expect(parseAliases([{ alias: "logs" }, { alias: "logs" }, { alias: "orders-current" }])).toEqual([
      "logs",
      "orders-current",
    ]);
  });

  it("rejects a body of another shape", () => {
    expect(() => parseAliases({ error: "x" })).toThrow();
  });
});
