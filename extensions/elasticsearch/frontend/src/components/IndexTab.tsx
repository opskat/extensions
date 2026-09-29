import { useState } from "react";
import { cn } from "@opskat/ui";
import { JsonTreeView } from "@opskat/host-ui";
import { esError } from "../es/errors";
import { formatBytes, formatNumber } from "../es/format";
import { DEFAULT_MAX_RESULT_WINDOW } from "../es/paging";
import { parseIndexSettings } from "../es/settings";
import type { IndexInfo } from "../es/types";
import { callTool, esRequest } from "../host";
import type { T } from "../i18n";
import { useAsync, type AsyncState } from "../useAsync";
import { DocumentsView } from "./DocumentsView";
import { HealthDot, InlineError, Spinner } from "./common";

type SubTab = "docs" | "mapping" | "settings";

/** One index: its shards, replicas, documents and size, and the documents / Mapping / Settings views. */
export function IndexTab({
  assetId,
  index,
  info,
  t,
  lang,
}: {
  assetId: number;
  index: string;
  /** The index's row in the loaded list; gone when a refresh no longer lists it. */
  info: IndexInfo | undefined;
  t: T;
  lang: string;
}) {
  const [sub, setSub] = useState<SubTab>("docs");
  // Mapping loads the first time it is shown, then stays.
  const [mappingWanted, setMappingWanted] = useState(false);

  const settings = useAsync(
    async (signal) => {
      const r = await esRequest(assetId, { method: "GET", path: `/${encodeURIComponent(index)}/_settings` }, signal);
      if (r.status < 200 || r.status >= 300) {
        const e = esError(r.body);
        throw new Error(`HTTP ${r.status}${e.type ? ` ${e.type}` : ""}: ${e.reason}`);
      }
      return { body: r.body, parsed: parseIndexSettings(r.body, index) };
    },
    [assetId, index]
  );
  const mapping = useAsync((signal) => callTool<unknown>("mapping", { index }, assetId, signal), [assetId, index], mappingWanted);

  const parsed = settings.state.status === "ready" ? settings.state.data.parsed : null;
  const stats = [
    parsed && t("page.index.primaries", { n: formatNumber(parsed.primaries, lang) }),
    parsed && t("page.index.replicas", { n: formatNumber(parsed.replicas, lang) }),
    info && info.docsCount !== null && t("page.index.docs", { n: formatNumber(info.docsCount, lang) }),
    info && info.storeSize !== null && formatBytes(info.storeSize),
  ].filter(Boolean);

  const subTabs: [SubTab, string][] = [
    ["docs", t("page.index.documents")],
    ["mapping", t("page.index.mapping")],
    ["settings", t("page.index.settings")],
  ];

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border px-3" role="tablist">
        {subTabs.map(([id, label]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={sub === id}
            onClick={() => {
              setSub(id);
              if (id === "mapping") setMappingWanted(true);
            }}
            className={cn(
              "-mb-px border-b-2 py-1.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring/45",
              sub === id
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {label}
          </button>
        ))}
        <span className="flex-1" />
        <span className="flex min-w-0 items-center gap-1.5 truncate text-[11px] text-muted-foreground">
          {info && <HealthDot health={info.health} />}
          {info?.status === "close" && <span>{t("page.index.closed")}</span>}
          <span className="truncate">{stats.join(" · ")}</span>
        </span>
      </div>
      <div className="relative min-h-0 flex-1">
        {/* Documents stays mounted so its query and page survive a look at the mapping. */}
        <div className={cn("absolute inset-0", sub !== "docs" && "invisible pointer-events-none")}>
          <DocumentsView
            assetId={assetId}
            index={index}
            maxResultWindow={parsed?.maxResultWindow ?? DEFAULT_MAX_RESULT_WINDOW}
            t={t}
            lang={lang}
          />
        </div>
        {sub === "mapping" && (
          <JsonPanel state={mapping.state} onRetry={mapping.reload} failedTitle={t("page.index.mappingFailed")} t={t} />
        )}
        {sub === "settings" && (
          <JsonPanel
            state={settings.state.status === "ready" ? { status: "ready", data: settings.state.data.body } : settings.state}
            onRetry={settings.reload}
            failedTitle={t("page.index.settingsFailed")}
            t={t}
          />
        )}
      </div>
    </div>
  );
}

/** A read-only JSON tree of a loaded response. */
function JsonPanel({
  state,
  onRetry,
  failedTitle,
  t,
}: {
  state: AsyncState<unknown>;
  onRetry: () => void;
  failedTitle: string;
  t: T;
}) {
  return (
    <div className="absolute inset-0 bg-background">
      {state.status === "loading" ? (
        <Spinner />
      ) : state.status === "error" ? (
        <InlineError title={failedTitle} detail={state.error} onRetry={onRetry} t={t} />
      ) : (
        <div className="h-full overflow-auto p-3 font-mono text-xs select-text">
          <JsonTreeView data={state.data} />
        </div>
      )}
    </div>
  );
}
