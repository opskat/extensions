import { describe, expect, it } from "vitest";
import { initialTabs, OVERVIEW_TAB_ID, parseSavedConsoles, savedConsoles, tabsReducer, type TabsState } from "./tabs";

const run = (...actions: Parameters<typeof tabsReducer>[1][]) => actions.reduce<TabsState>(tabsReducer, initialTabs);
const ids = (s: TabsState) => s.tabs.map((t) => t.id);

describe("tabsReducer", () => {
  it("starts with only the overview, active", () => {
    expect(ids(initialTabs)).toEqual([OVERVIEW_TAB_ID]);
    expect(initialTabs.activeId).toBe(OVERVIEW_TAB_ID);
  });

  it("opens an index once and switches to it when opened again", () => {
    const s = run({ type: "openIndex", index: "logs" }, { type: "openIndex", index: "orders" }, { type: "openIndex", index: "logs" });
    expect(s.tabs.filter((t) => t.kind === "index").map((t) => t.kind === "index" && t.index)).toEqual(["logs", "orders"]);
    expect(s.tabs.find((t) => t.id === s.activeId)).toMatchObject({ kind: "index", index: "logs" });
  });

  it("opens a new, numbered console every time", () => {
    const s = run({ type: "openConsole" }, { type: "openConsole" });
    const consoles = s.tabs.filter((t) => t.kind === "console");
    expect(consoles.map((t) => t.kind === "console" && t.number)).toEqual([1, 2]);
    expect(new Set(consoles.map((t) => t.id)).size).toBe(2);
    expect(s.activeId).toBe(consoles[1].id);
  });

  it("never closes the overview", () => {
    expect(run({ type: "close", id: OVERVIEW_TAB_ID })).toEqual(initialTabs);
  });

  it("activates the right-hand neighbour when the active tab closes, else the left one", () => {
    let s = run({ type: "openIndex", index: "a" }, { type: "openIndex", index: "b" }, { type: "openIndex", index: "c" });
    const [, a, b, c] = s.tabs;
    s = tabsReducer(tabsReducer(s, { type: "activate", id: b.id }), { type: "close", id: b.id });
    expect(s.activeId).toBe(c.id);
    s = tabsReducer(s, { type: "close", id: c.id });
    expect(s.activeId).toBe(a.id);
  });

  it("keeps the active tab when another one closes", () => {
    let s = run({ type: "openIndex", index: "a" }, { type: "openConsole" });
    const active = s.activeId;
    s = tabsReducer(s, { type: "close", id: s.tabs[1].id });
    expect(s.activeId).toBe(active);
    expect(s.tabs).toHaveLength(2);
  });
});

describe("console persistence", () => {
  const consoles = (s: TabsState) => s.tabs.flatMap((t) => (t.kind === "console" ? [t] : []));

  it("snapshots the open consoles in tab order with their current text; index tabs are left out", () => {
    const s = run({ type: "openConsole" }, { type: "openIndex", index: "logs" }, { type: "openConsole" });
    const [c1, c2] = consoles(s);
    const texts = new Map([[c2.id, "GET /_cat/nodes"]]);
    expect(savedConsoles(s, texts)).toEqual([
      { number: 1, text: "" },
      { number: 2, text: "GET /_cat/nodes" },
    ]);
    expect(c1.initialText).toBe("");
  });

  it("restores saved consoles with their numbers and text, and keeps the overview active", () => {
    const s = run({
      type: "restoreConsoles",
      consoles: [
        { number: 3, text: "GET /_search" },
        { number: 1, text: "" },
      ],
    });
    expect(consoles(s).map((c) => [c.number, c.initialText])).toEqual([
      [3, "GET /_search"],
      [1, ""],
    ]);
    expect(s.activeId).toBe(OVERVIEW_TAB_ID);
    // A new console never reuses a restored number.
    expect(consoles(tabsReducer(s, { type: "openConsole" })).at(-1)?.number).toBe(4);
  });

  it("round-trips: what a page saved is what the next page restores", () => {
    const before = run({ type: "openConsole" }, { type: "openConsole" }, { type: "close", id: "console:1" });
    const saved = savedConsoles(before, new Map([["console:2", "PUT /orders"]]));
    const after = run({ type: "restoreConsoles", consoles: parseSavedConsoles(JSON.parse(JSON.stringify({ consoles: saved }))) });
    expect(savedConsoles(after, new Map())).toEqual(saved);
  });

  it("renumbers a restored console whose number a console opened meanwhile already has", () => {
    const s = run({ type: "openConsole" }, { type: "restoreConsoles", consoles: [{ number: 1, text: "GET /" }] });
    const numbers = consoles(s).map((c) => c.number);
    expect(new Set(numbers).size).toBe(2);
    expect(new Set(consoles(s).map((c) => c.id)).size).toBe(2);
    expect(consoles(s).find((c) => c.initialText === "GET /")?.number).toBe(2);
  });

  it("reads the load action's answer, and refuses an answer of another shape", () => {
    expect(parseSavedConsoles({ consoles: [{ number: 2, text: "x" }] })).toEqual([{ number: 2, text: "x" }]);
    expect(parseSavedConsoles({ consoles: [] })).toEqual([]);
    for (const bad of [null, {}, { consoles: {} }, { consoles: [{ number: "1", text: "" }] }, { consoles: [{ number: 1 }] }]) {
      expect(() => parseSavedConsoles(bad)).toThrow();
    }
  });
});
