import { useMemo, useState } from "react";
import { Eye, EyeOff, RefreshCw, Search, Server } from "lucide-react";
import { Button, Input, Switch, cn } from "@opskat/ui";
import { formatBytes, formatCount, formatNumber } from "../es/format";
import { isSystemIndex, visibleIndices } from "../es/indices";
import type { ClusterHealth, IndexInfo } from "../es/types";
import type { T } from "../i18n";
import { HealthBadge, HealthDot, SkeletonBar } from "./common";

/** Cluster name, health, version, node and index counts, and the page's refresh. */
export function ClusterHeader({
  health,
  indexCount,
  refreshing,
  onRefresh,
  t,
  lang,
}: {
  health: ClusterHealth | null;
  indexCount: number;
  refreshing: boolean;
  onRefresh: () => void;
  t: T;
  lang: string;
}) {
  const loading = health === null;
  return (
    <div className="shrink-0 space-y-1 border-b border-border px-3 py-2.5">
      <div className="flex items-center gap-2">
        <Server className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        {loading ? (
          <SkeletonBar className="h-3.5 w-28" />
        ) : (
          <span className="min-w-0 truncate text-sm font-semibold select-text" title={health.clusterName}>
            {health.clusterName}
          </span>
        )}
        <span className="flex-1" />
        {!loading && <HealthBadge health={health.status} />}
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={onRefresh}
          disabled={loading || refreshing}
          aria-label={t("page.action.refresh")}
          title={t("page.action.refresh")}
        >
          <RefreshCw className={cn((loading || refreshing) && "animate-spin")} />
        </Button>
      </div>
      {loading ? (
        <SkeletonBar className="h-3 w-36" />
      ) : (
        <div className="truncate text-[11px] text-muted-foreground">
          {t("page.cluster.summary", {
            version: health.version,
            nodes: formatNumber(health.numberOfNodes, lang),
            indices: formatNumber(indexCount, lang),
          })}
        </div>
      )}
    </div>
  );
}

/** The index list: filter, system-index toggle, one row per index; a click opens its tab. */
export function IndexList({
  indices,
  selected,
  onOpen,
  t,
  lang,
}: {
  indices: IndexInfo[] | null;
  selected: string | null;
  onOpen: (index: string) => void;
  t: T;
  lang: string;
}) {
  const [filter, setFilter] = useState("");
  const [showSystem, setShowSystem] = useState(false);
  const rows = useMemo(
    () => (indices ? visibleIndices(indices, { filter, showSystem }) : []),
    [indices, filter, showSystem]
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center justify-between gap-2 px-3 pt-2 pb-1">
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          {t("page.indices.title")}
        </span>
        <label
          className="flex items-center gap-1.5 text-[11px] text-muted-foreground"
          title={t("page.indices.systemHint")}
        >
          {showSystem ? <Eye className="size-3" aria-hidden /> : <EyeOff className="size-3" aria-hidden />}
          {t("page.indices.system")}
          <Switch checked={showSystem} onCheckedChange={setShowSystem} className="scale-75" />
        </label>
      </div>
      <div className="shrink-0 px-2 pb-1.5">
        <div className="relative">
          <Search className="absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden />
          <Input
            className="h-7 pl-7 text-xs"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t("page.indices.filter")}
            aria-label={t("page.indices.filter")}
            disabled={!indices}
          />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!indices ? (
          Array.from({ length: 6 }, (_, i) => (
            <SkeletonBar key={i} className="mx-1 my-2 h-4" style={{ width: `${60 + ((i * 13) % 35)}%` }} />
          ))
        ) : rows.length === 0 ? (
          <div className="px-2 py-6 text-center text-xs text-muted-foreground">
            {filter.trim()
              ? t("page.indices.noMatch")
              : indices.length > 0
                ? t("page.indices.onlySystem")
                : t("page.indices.none")}
          </div>
        ) : (
          rows.map((r) => (
            <button
              key={r.index}
              type="button"
              onClick={() => onOpen(r.index)}
              aria-current={r.index === selected ? "true" : undefined}
              title={t("page.indices.rowTitle", {
                name: r.index,
                health: r.health || "—",
                status: r.status,
                docs: r.docsCount === null ? "—" : formatNumber(r.docsCount, lang),
                size: formatBytes(r.storeSize),
              })}
              className={cn(
                "flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-left text-xs transition-colors outline-none focus-visible:ring-1 focus-visible:ring-ring/45",
                r.index === selected ? "bg-primary/10 text-foreground" : "hover:bg-sidebar-accent"
              )}
            >
              <HealthDot health={r.health} />
              <span className={cn("min-w-0 flex-1 truncate", isSystemIndex(r.index) && "text-muted-foreground")}>
                {r.index}
              </span>
              <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                {formatCount(r.docsCount, lang)}
              </span>
              <span className="w-14 shrink-0 text-right font-mono text-[10px] text-muted-foreground">
                {formatBytes(r.storeSize)}
              </span>
            </button>
          ))
        )}
      </div>
    </div>
  );
}
