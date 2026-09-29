import { describe, expect, it } from "vitest";
import { clusterTotals, nonGreenIndices, parseNodes } from "./cluster";
import type { IndexInfo } from "./types";

const idx = (index: string, health: string, docsCount: number | null, storeSize: number | null): IndexInfo => ({
  index,
  health,
  status: docsCount === null ? "close" : "open",
  docsCount,
  storeSize,
});

describe("clusterTotals", () => {
  it("sums documents and store size over the non-system indices, skipping closed ones", () => {
    const totals = clusterTotals([
      idx("a", "green", 10, 1000),
      idx("b", "yellow", 5, 500),
      idx(".kibana", "green", 99, 9999),
      idx("closed", "red", null, null),
    ]);
    expect(totals).toEqual({ docs: 15, bytes: 1500 });
  });
});

describe("nonGreenIndices", () => {
  it("lists the yellow and red indices, system ones included, red first then by name", () => {
    const list = nonGreenIndices([
      idx("b-yellow", "yellow", 1, 1),
      idx("ok", "green", 1, 1),
      idx("z-red", "red", 1, 1),
      idx(".sys-yellow", "yellow", 1, 1),
      idx("a-yellow", "yellow", 1, 1),
    ]);
    expect(list.map((i) => i.index)).toEqual(["z-red", ".sys-yellow", "a-yellow", "b-yellow"]);
  });
});

describe("parseNodes", () => {
  it("reads _cat/nodes rows: the elected master is marked, percentages are numbers", () => {
    const nodes = parseNodes([
      { name: "n2", ip: "10.0.0.2", "node.role": "dimr", master: "-", "heap.percent": "55", cpu: "12", "disk.used_percent": "39.52" },
      { name: "n1", ip: "10.0.0.1", "node.role": "cdfhilmrstw", master: "*", "heap.percent": "62", cpu: "18", "disk.used_percent": null },
    ]);
    expect(nodes).toEqual([
      { name: "n1", ip: "10.0.0.1", roles: "cdfhilmrstw", master: true, heapPercent: 62, cpuPercent: 18, diskPercent: null },
      { name: "n2", ip: "10.0.0.2", roles: "dimr", master: false, heapPercent: 55, cpuPercent: 12, diskPercent: 39.52 },
    ]);
  });

  it("refuses a response that is not a list of rows", () => {
    expect(() => parseNodes({ error: "x" })).toThrow();
  });
});
