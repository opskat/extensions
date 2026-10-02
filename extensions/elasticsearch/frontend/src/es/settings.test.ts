import { describe, expect, it } from "vitest";
import { parseIndexSettings } from "./settings";

const body = (index: Record<string, unknown>) => ({ logs: { settings: { index } } });

describe("parseIndexSettings", () => {
  it("reads primary shards, replicas and the result window of the index", () => {
    const s = parseIndexSettings(
      body({ number_of_shards: "3", number_of_replicas: "1", max_result_window: "50000" }),
      "logs"
    );
    expect(s).toEqual({ primaries: 3, replicas: 1, maxResultWindow: 50000 });
  });

  it("uses Elasticsearch's default window when the index does not set one", () => {
    expect(parseIndexSettings(body({ number_of_shards: "1", number_of_replicas: "0" }), "logs").maxResultWindow).toBe(10000);
  });

  it("refuses a body that has no settings for the index", () => {
    expect(() => parseIndexSettings({ other: {} }, "logs")).toThrow();
  });
});
