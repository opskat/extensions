import { isSystemIndex } from "./indices";
import type { IndexInfo } from "./types";

/** Documents and store size of the user's data: system indices and closed indices are left out. */
export function clusterTotals(indices: IndexInfo[]): { docs: number; bytes: number } {
  let docs = 0;
  let bytes = 0;
  for (const i of indices) {
    if (isSystemIndex(i.index)) continue;
    docs += i.docsCount ?? 0;
    bytes += i.storeSize ?? 0;
  }
  return { docs, bytes };
}

const healthRank: Record<string, number> = { red: 0, yellow: 1 };

/** The indices that keep the cluster from green, red first. */
export function nonGreenIndices(indices: IndexInfo[]): IndexInfo[] {
  return indices
    .filter((i) => i.health in healthRank)
    .sort((a, b) => healthRank[a.health] - healthRank[b.health] || a.index.localeCompare(b.index));
}

/** The `_cat/nodes` columns the overview asks for. Available alike on 7.10+, 8.x and 9.x. */
export const NODE_COLUMNS = "name,ip,node.role,master,heap.percent,cpu,disk.used_percent";

export interface NodeInfo {
  name: string;
  ip: string;
  roles: string;
  /** The elected master. */
  master: boolean;
  heapPercent: number | null;
  cpuPercent: number | null;
  diskPercent: number | null;
}

type CatValue = string | null | undefined;

function percent(v: CatValue): number | null {
  if (v === null || v === undefined || v === "") return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

/** Reads a `_cat/nodes?format=json&h=<NODE_COLUMNS>` body, master first then by name. */
export function parseNodes(body: unknown): NodeInfo[] {
  if (!Array.isArray(body)) throw new Error(`unexpected _cat/nodes response: ${JSON.stringify(body)}`);
  return (body as Record<string, CatValue>[])
    .map((r) => ({
      name: r.name ?? "",
      ip: r.ip ?? "",
      roles: r["node.role"] ?? "",
      master: r.master === "*",
      heapPercent: percent(r["heap.percent"]),
      cpuPercent: percent(r.cpu),
      diskPercent: percent(r["disk.used_percent"]),
    }))
    .sort((a, b) => Number(b.master) - Number(a.master) || a.name.localeCompare(b.name));
}
