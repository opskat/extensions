import { AlertCircle, Server } from "lucide-react";
import { cn } from "@opskat/ui";
import { clusterTotals, nonGreenIndices, type NodeInfo } from "../es/cluster";
import { formatBytes, formatCount, formatNumber } from "../es/format";
import type { T } from "../i18n";
import type { ClusterData } from "../useCluster";
import { HealthDot, SkeletonBar, healthText } from "./common";

const NON_GREEN_SHOWN = 20;

/** Cluster status, nodes, shards and data totals; the nodes table; why the cluster is not green. */
export function Overview({ data, t, lang }: { data: ClusterData | null; t: T; lang: string }) {
  if (!data) return <OverviewSkeleton />;
  const { health, indices, nodes } = data;
  const totals = clusterTotals(indices);
  const master = nodes.ok ? nodes.list.find((n) => n.master)?.name : undefined;
  const totalShards = health.activeShards + health.unassignedShards;

  return (
    <div className="@container h-full space-y-4 overflow-y-auto p-4">
      <div className="grid grid-cols-2 gap-3 @3xl:grid-cols-4">
        <Stat
          label={t("page.overview.status")}
          value={health.status}
          valueClass={healthText(health.status)}
          hint={
            health.unassignedShards > 0
              ? t("page.overview.unassigned", { n: formatNumber(health.unassignedShards, lang) })
              : t("page.overview.allAssigned")
          }
        />
        <Stat
          label={t("page.overview.nodes")}
          value={formatNumber(health.numberOfNodes, lang)}
          hint={t("page.overview.nodesHint", {
            master: master ?? "—",
            data: formatNumber(health.numberOfDataNodes, lang),
          })}
        />
        <Stat
          label={t("page.overview.shards")}
          value={`${formatNumber(health.activeShards, lang)} / ${formatNumber(totalShards, lang)}`}
          hint={t("page.overview.shardsHint", {
            primary: formatNumber(health.activePrimaryShards, lang),
            unassigned: formatNumber(health.unassignedShards, lang),
          })}
        />
        <Stat
          label={t("page.overview.data")}
          value={`${formatCount(totals.docs, lang)} / ${formatBytes(totals.bytes)}`}
          hint={t("page.overview.dataHint")}
        />
      </div>
      {health.status !== "green" && <NotGreen status={health.status} data={data} t={t} />}
      <NodesTable nodes={nodes} t={t} />
    </div>
  );
}

function Stat({ label, value, hint, valueClass }: { label: string; value: string; hint: string; valueClass?: string }) {
  return (
    <div className="rounded-lg border border-border bg-card px-3 py-2.5">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className={cn("mt-0.5 truncate text-lg font-semibold select-text", valueClass)}>{value}</div>
      <div className="mt-0.5 truncate text-[10px] text-muted-foreground" title={hint}>
        {hint}
      </div>
    </div>
  );
}

function NotGreen({ status, data, t }: { status: string; data: ClusterData; t: T }) {
  const bad = nonGreenIndices(data.indices);
  const red = status === "red";
  return (
    <div
      role="status"
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-xs",
        red ? "border-destructive/30 bg-destructive/10" : "border-warning/30 bg-warning/10"
      )}
    >
      <AlertCircle className={cn("mt-0.5 size-3.5 shrink-0", healthText(status))} aria-hidden />
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className={cn("font-medium", healthText(status))}>{t("page.overview.notGreen", { status })}</div>
        {bad.length === 0 ? (
          <div className="text-muted-foreground">{t("page.overview.notGreenNoIndex")}</div>
        ) : (
          <ul className="space-y-1">
            {bad.slice(0, NON_GREEN_SHOWN).map((i) => (
              <li key={i.index} className="flex items-start gap-2">
                <HealthDot health={i.health} className="mt-1" />
                <span className="shrink-0 font-mono select-text">{i.index}</span>
                <span className="text-muted-foreground">
                  {i.health === "red" ? t("page.overview.redReason") : t("page.overview.yellowReason")}
                </span>
              </li>
            ))}
            {bad.length > NON_GREEN_SHOWN && (
              <li className="text-muted-foreground">
                {t("page.overview.moreIndices", { n: bad.length - NON_GREEN_SHOWN })}
              </li>
            )}
          </ul>
        )}
      </div>
    </div>
  );
}

function NodesTable({ nodes, t }: { nodes: ClusterData["nodes"]; t: T }) {
  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-xs font-semibold">
        <Server className="size-3.5 text-muted-foreground" aria-hidden />
        {t("page.overview.nodesTable")}
      </div>
      {!nodes.ok ? (
        <div className="px-3 py-3 text-xs">
          <div className="text-destructive">{t("page.overview.nodesFailed")}</div>
          <div className="mt-1 font-mono text-[11px] break-all text-muted-foreground select-text">{nodes.error}</div>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="text-muted-foreground">
              <tr className="border-b border-border">
                <th className="px-3 py-1.5 text-left font-medium">{t("page.overview.node.name")}</th>
                <th className="px-3 py-1.5 text-left font-medium">{t("page.overview.node.ip")}</th>
                <th className="px-3 py-1.5 text-left font-medium" title={t("page.overview.node.rolesHint")}>
                  {t("page.overview.node.roles")}
                </th>
                <th className="px-3 py-1.5 text-left font-medium">{t("page.overview.node.heap")}</th>
                <th className="px-3 py-1.5 text-left font-medium">{t("page.overview.node.cpu")}</th>
                <th className="px-3 py-1.5 text-left font-medium">{t("page.overview.node.disk")}</th>
              </tr>
            </thead>
            <tbody>
              {nodes.list.map((n) => (
                <NodeRow key={`${n.name}@${n.ip}`} node={n} t={t} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function NodeRow({ node, t }: { node: NodeInfo; t: T }) {
  return (
    <tr className="border-b border-border last:border-0">
      <td className="px-3 py-1.5 whitespace-nowrap select-text">
        {node.name}
        {node.master && (
          <span className="ml-1.5 rounded-sm bg-primary/10 px-1 text-[10px] text-primary">
            {t("page.overview.node.master")}
          </span>
        )}
      </td>
      <td className="px-3 py-1.5 font-mono text-muted-foreground select-text">{node.ip}</td>
      <td className="px-3 py-1.5 font-mono text-muted-foreground">{node.roles}</td>
      <td className="px-3 py-1.5">
        <Bar value={node.heapPercent} />
      </td>
      <td className="px-3 py-1.5">
        <Bar value={node.cpuPercent} />
      </td>
      <td className="px-3 py-1.5">
        <Bar value={node.diskPercent} />
      </td>
    </tr>
  );
}

function Bar({ value }: { value: number | null }) {
  if (value === null) return <span className="text-muted-foreground">—</span>;
  const tone = value > 85 ? "bg-destructive" : value > 70 ? "bg-warning" : "bg-primary";
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
        <div className={cn("h-full rounded-full", tone)} style={{ width: `${Math.min(100, value)}%` }} />
      </div>
      <span className="font-mono text-[10px] text-muted-foreground">{Math.round(value)}%</span>
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="@container h-full space-y-4 overflow-y-auto p-4" aria-busy="true">
      <div className="grid grid-cols-2 gap-3 @3xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="space-y-2 rounded-lg border border-border px-3 py-2.5">
            <SkeletonBar className="h-3 w-16" />
            <SkeletonBar className="h-5 w-24" />
            <SkeletonBar className="h-2.5 w-32" />
          </div>
        ))}
      </div>
      <div className="space-y-2 rounded-lg border border-border p-3">
        {Array.from({ length: 3 }, (_, i) => (
          <SkeletonBar key={i} className="h-4 w-full" />
        ))}
      </div>
    </div>
  );
}
