import { describe, expect, it } from "vitest";
import { initialTabs, OVERVIEW_TAB_ID, tabsReducer, type TabsState } from "./tabs";

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
