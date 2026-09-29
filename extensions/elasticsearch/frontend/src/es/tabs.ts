// The page's own tab model: the overview (always there), one tab per opened
// index, and any number of consoles. Kept serialisable — plain ids, kinds and
// numbers — so the console task can persist the console tabs per asset without
// reshaping it.

export const OVERVIEW_TAB_ID = "overview";

export type EsTab =
  | { id: typeof OVERVIEW_TAB_ID; kind: "overview" }
  | { id: string; kind: "index"; index: string }
  | { id: string; kind: "console"; number: number };

export type ConsoleTabModel = Extract<EsTab, { kind: "console" }>;

export interface TabsState {
  tabs: EsTab[];
  activeId: string;
  /** Number the next console gets; never reused while the page is open. */
  nextConsole: number;
}

export type TabsAction =
  | { type: "openIndex"; index: string }
  | { type: "openConsole" }
  | { type: "activate"; id: string }
  | { type: "close"; id: string };

export const initialTabs: TabsState = {
  tabs: [{ id: OVERVIEW_TAB_ID, kind: "overview" }],
  activeId: OVERVIEW_TAB_ID,
  nextConsole: 1,
};

export function tabsReducer(state: TabsState, action: TabsAction): TabsState {
  switch (action.type) {
    case "openIndex": {
      const id = `index:${action.index}`;
      if (state.tabs.some((t) => t.id === id)) return { ...state, activeId: id };
      return { ...state, tabs: [...state.tabs, { id, kind: "index", index: action.index }], activeId: id };
    }
    case "openConsole": {
      const id = `console:${state.nextConsole}`;
      return {
        tabs: [...state.tabs, { id, kind: "console", number: state.nextConsole }],
        activeId: id,
        nextConsole: state.nextConsole + 1,
      };
    }
    case "activate":
      return state.tabs.some((t) => t.id === action.id) ? { ...state, activeId: action.id } : state;
    case "close": {
      const at = state.tabs.findIndex((t) => t.id === action.id);
      if (at < 0 || action.id === OVERVIEW_TAB_ID) return state;
      const tabs = state.tabs.filter((t) => t.id !== action.id);
      const activeId = state.activeId === action.id ? (tabs[at] ?? tabs[at - 1]).id : state.activeId;
      return { ...state, tabs, activeId };
    }
  }
}
