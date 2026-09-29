// The page's own tab model: the overview (always there), one tab per opened
// index, and any number of consoles. Plain serialisable data: the console tabs
// are saved per asset (savedConsoles → the console.save action) and restored
// when the asset is opened again (console.load → restoreConsoles); index tabs
// are not.

export const OVERVIEW_TAB_ID = "overview";

export type EsTab =
  | { id: typeof OVERVIEW_TAB_ID; kind: "overview" }
  | { id: string; kind: "index"; index: string }
  /** initialText: what the console's editor starts with — the saved text of a restored console. */
  | { id: string; kind: "console"; number: number; initialText: string };

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
  | { type: "close"; id: string }
  | { type: "restoreConsoles"; consoles: SavedConsole[] };

/** One console as the console.save / console.load actions carry it (console.go). */
export interface SavedConsole {
  number: number;
  text: string;
}

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
      const id = consoleId(state.nextConsole);
      return {
        tabs: [...state.tabs, { id, kind: "console", number: state.nextConsole, initialText: "" }],
        activeId: id,
        nextConsole: state.nextConsole + 1,
      };
    }
    case "restoreConsoles": {
      // Appended after what is open; a number a console opened meanwhile already
      // has goes to the restored one's next free number instead.
      const taken = new Set(state.tabs.flatMap((t) => (t.kind === "console" ? [t.number] : [])));
      let next = Math.max(state.nextConsole, ...action.consoles.map((c) => c.number + 1));
      const restored = action.consoles.map((c): ConsoleTabModel => {
        const number = taken.has(c.number) ? next++ : c.number;
        taken.add(number);
        return { id: consoleId(number), kind: "console", number, initialText: c.text };
      });
      return { ...state, tabs: [...state.tabs, ...restored], nextConsole: next };
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

function consoleId(number: number): string {
  return `console:${number}`;
}

/**
 * The open consoles in tab order, as console.save stores them: `texts` holds
 * each console's current editor text by tab id, a console not in it (never
 * edited) its initial text.
 */
export function savedConsoles(state: TabsState, texts: ReadonlyMap<string, string>): SavedConsole[] {
  return state.tabs.flatMap((t) =>
    t.kind === "console" ? [{ number: t.number, text: texts.get(t.id) ?? t.initialText }] : []
  );
}

/** Reads console.load's result; any other shape is an error, not an empty list. */
export function parseSavedConsoles(result: unknown): SavedConsole[] {
  const consoles = (result as { consoles?: unknown } | null)?.consoles;
  if (!Array.isArray(consoles)) throw new Error(`unexpected console.load result: ${JSON.stringify(result)}`);
  return consoles.map((c: { number?: unknown; text?: unknown }) => {
    if (!Number.isInteger(c?.number) || typeof c?.text !== "string") {
      throw new Error(`unexpected saved console: ${JSON.stringify(c)}`);
    }
    return { number: c.number as number, text: c.text };
  });
}
