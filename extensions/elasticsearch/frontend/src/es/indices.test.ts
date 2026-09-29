import { describe, expect, it } from "vitest";
import { isSystemIndex, visibleIndices } from "./indices";
import type { IndexInfo } from "./types";

const idx = (index: string): IndexInfo => ({ index, health: "green", status: "open", docsCount: 1, storeSize: 1 });
const names = (list: IndexInfo[]) => list.map((i) => i.index);
const all = [idx("logs-app"), idx("orders"), idx(".kibana_1"), idx("logs-web"), idx(".security-7")];

describe("isSystemIndex", () => {
  it("is an index whose name starts with a dot", () => {
    expect(isSystemIndex(".kibana_1")).toBe(true);
    expect(isSystemIndex("logs.2026")).toBe(false);
  });
});

describe("visibleIndices", () => {
  it("hides system indices unless asked to show them", () => {
    expect(names(visibleIndices(all, { filter: "", showSystem: false }))).toEqual(["logs-app", "orders", "logs-web"]);
    expect(names(visibleIndices(all, { filter: "", showSystem: true }))).toHaveLength(5);
  });

  it("keeps the names containing the filter text, ignoring case and surrounding space", () => {
    expect(names(visibleIndices(all, { filter: " LOGS ", showSystem: false }))).toEqual(["logs-app", "logs-web"]);
    expect(names(visibleIndices(all, { filter: "kib", showSystem: false }))).toEqual([]);
    expect(names(visibleIndices(all, { filter: "kib", showSystem: true }))).toEqual([".kibana_1"]);
  });
});
