import { useReducer } from "react";
import { cn, useResizeHandle } from "@opskat/ui";
import { ROOT_CLASS } from "../../tooling/scope-css";
import type { CompletionSource } from "../es/completionSource";
import { initialTabs, tabsReducer, type EsTab } from "../es/tabs";
import { useT, type T } from "../i18n";
import { useCluster, type ClusterData } from "../useCluster";
import { useCompletionSource } from "../useCompletionSource";
import { useConsolePersistence, type ConsolePersistence } from "../useConsolePersistence";
import { ConsoleTab } from "./console/ConsoleTab";
import { IndexTab } from "./IndexTab";
import { Overview } from "./Overview";
import { PageError } from "./PageError";
import { ClusterHeader, IndexList } from "./Sidebar";
import { TabStrip } from "./TabStrip";

/** The page opening an Elasticsearch asset shows (slot asset.connect). */
export function ElasticsearchPage({ assetId }: { assetId: number }) {
  const { t, lang } = useT();
  const { state, reload } = useCluster(assetId);
  const [tabs, dispatch] = useReducer(tabsReducer, initialTabs);
  const consoles = useConsolePersistence(assetId, tabs, dispatch);
  const data = state.status === "ready" ? state.data : null;
  const completion = useCompletionSource(assetId, data);
  const sidebar = useResizeHandle({
    defaultSize: 260,
    minSize: 200,
    maxSize: 480,
    storageKey: "ext-elasticsearch:sidebar-width",
  });

  if (state.status === "error") {
    return (
      <div className={cn(ROOT_CLASS, "h-full w-full")}>
        <PageError error={state.error} onRetry={reload} t={t} />
      </div>
    );
  }

  const active = tabs.tabs.find((tab) => tab.id === tabs.activeId);

  return (
    // ROOT_CLASS: the page's stylesheet applies inside this element only.
    <div className={cn(ROOT_CLASS, "flex h-full w-full bg-background text-foreground")}>
      <aside className="flex h-full shrink-0 flex-col border-r border-border bg-sidebar" style={{ width: sidebar.size }}>
        <ClusterHeader
          health={data?.health ?? null}
          indexCount={data?.indices.length ?? 0}
          refreshing={state.status === "ready" && state.refreshing}
          onRefresh={reload}
          t={t}
          lang={lang}
        />
        <IndexList
          indices={data?.indices ?? null}
          selected={active?.kind === "index" ? active.index : null}
          onOpen={(index) => dispatch({ type: "openIndex", index })}
          t={t}
          lang={lang}
        />
      </aside>
      <div
        role="separator"
        aria-orientation="vertical"
        onMouseDown={sidebar.handleMouseDown}
        className={cn(
          "-ml-1 w-1 shrink-0 cursor-col-resize transition-colors hover:bg-primary/50",
          sidebar.isResizing && "bg-primary/50"
        )}
      />
      <div className="flex h-full min-w-0 flex-1 flex-col">
        <TabStrip state={tabs} dispatch={dispatch} t={t} />
        <div className="relative min-h-0 flex-1">
          {tabs.tabs.map((tab) => (
            // Every tab stays mounted: switching away keeps a query, its page and an editor as they were.
            <div
              key={tab.id}
              role="tabpanel"
              className={cn("absolute inset-0", tab.id !== tabs.activeId && "invisible pointer-events-none")}
            >
              <TabContent
                tab={tab}
                assetId={assetId}
                data={data}
                completion={completion}
                consoles={consoles}
                t={t}
                lang={lang}
              />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function TabContent({
  tab,
  assetId,
  data,
  completion,
  consoles,
  t,
  lang,
}: {
  tab: EsTab;
  assetId: number;
  data: ClusterData | null;
  completion: CompletionSource;
  consoles: ConsolePersistence;
  t: T;
  lang: string;
}) {
  switch (tab.kind) {
    case "overview":
      return <Overview data={data} t={t} lang={lang} />;
    case "index":
      return (
        <IndexTab
          assetId={assetId}
          index={tab.index}
          info={data?.indices.find((i) => i.index === tab.index)}
          completion={completion}
          t={t}
          lang={lang}
        />
      );
    case "console":
      return (
        <ConsoleTab
          assetId={assetId}
          tab={tab}
          completion={completion}
          consoles={consoles}
          t={t}
          lang={lang}
        />
      );
  }
}
