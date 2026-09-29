import type { Dispatch } from "react";
import { Code2, Gauge, Plus, Table2, X } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import { OVERVIEW_TAB_ID, type EsTab, type TabsAction, type TabsState } from "../es/tabs";
import type { T } from "../i18n";

export function tabTitle(tab: EsTab, t: T): string {
  switch (tab.kind) {
    case "overview":
      return t("page.tabs.overview");
    case "index":
      return tab.index;
    case "console":
      return t("page.tabs.console", { number: tab.number });
  }
}

const icons = { overview: Gauge, index: Table2, console: Code2 };

/** The page's tab strip: overview first and pinned, index and console tabs closable, "+ Console". */
export function TabStrip({ state, dispatch, t }: { state: TabsState; dispatch: Dispatch<TabsAction>; t: T }) {
  return (
    <div className="flex shrink-0 items-center overflow-x-auto border-b border-border bg-muted/30" role="tablist">
      {state.tabs.map((tab) => {
        const Icon = icons[tab.kind];
        const active = tab.id === state.activeId;
        const title = tabTitle(tab, t);
        return (
          <div
            key={tab.id}
            className={cn(
              "flex shrink-0 items-center border-r border-border text-xs",
              active ? "bg-background text-foreground" : "text-muted-foreground hover:bg-background/50"
            )}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active}
              title={title}
              onClick={() => dispatch({ type: "activate", id: tab.id })}
              onAuxClick={(e) => {
                if (e.button === 1 && tab.id !== OVERVIEW_TAB_ID) dispatch({ type: "close", id: tab.id });
              }}
              className={cn(
                "flex cursor-pointer items-center gap-1.5 py-1.5 pl-3 whitespace-nowrap outline-none focus-visible:ring-1 focus-visible:ring-ring/45",
                tab.id === OVERVIEW_TAB_ID ? "pr-3" : "pr-1"
              )}
            >
              <Icon className="size-3 shrink-0" aria-hidden />
              <span className="max-w-[160px] truncate">{title}</span>
            </button>
            {tab.id !== OVERVIEW_TAB_ID && (
              <button
                type="button"
                onClick={() => dispatch({ type: "close", id: tab.id })}
                aria-label={t("page.tabs.close", { title })}
                title={t("page.tabs.close", { title })}
                className="mr-1.5 rounded-sm p-0.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring/45"
              >
                <X className="size-3" aria-hidden />
              </button>
            )}
          </div>
        );
      })}
      <Button
        variant="ghost"
        size="xs"
        className="ml-1 shrink-0 text-muted-foreground"
        onClick={() => dispatch({ type: "openConsole" })}
      >
        <Plus aria-hidden />
        {t("page.tabs.newConsole")}
      </Button>
    </div>
  );
}
