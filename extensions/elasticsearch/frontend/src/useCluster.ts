import { useCallback, useEffect, useRef, useState } from "react";
import { callTool, esRequest } from "./host";
import { NODE_COLUMNS, parseNodes, type NodeInfo } from "./es/cluster";
import { errorMessage, esError } from "./es/errors";
import type { ClusterHealth, IndexInfo } from "./es/types";

export interface ClusterData {
  health: ClusterHealth;
  /** Every index, system ones included; the sidebar decides what to show. */
  indices: IndexInfo[];
  /** The nodes table fails on its own: a user without node monitoring rights still gets the page. */
  nodes: { ok: true; list: NodeInfo[] } | { ok: false; error: string };
}

export type ClusterState =
  | { status: "loading" }
  | { status: "error"; error: string }
  | { status: "ready"; data: ClusterData; refreshing: boolean };

async function loadNodes(assetId: number, signal: AbortSignal): Promise<ClusterData["nodes"]> {
  try {
    const r = await esRequest(assetId, { method: "GET", path: `/_cat/nodes?format=json&h=${NODE_COLUMNS}` }, signal);
    if (r.status < 200 || r.status >= 300) {
      const e = esError(r.body);
      return { ok: false, error: `HTTP ${r.status}${e.type ? ` ${e.type}` : ""}: ${e.reason}` };
    }
    return { ok: true, list: parseNodes(r.body) };
  } catch (err) {
    if (signal.aborted) throw err;
    return { ok: false, error: errorMessage(err) };
  }
}

async function loadCluster(assetId: number, signal: AbortSignal): Promise<ClusterData> {
  const [health, indices, nodes] = await Promise.all([
    callTool<ClusterHealth>("health", {}, assetId, signal),
    callTool<{ indices: IndexInfo[] }>("indices", { "include-hidden": true }, assetId, signal),
    loadNodes(assetId, signal),
  ]);
  return { health, indices: indices.indices, nodes };
}

/**
 * The cluster the page shows: health, the index list and the nodes. A refresh
 * keeps the loaded data on screen while it runs; a cluster that cannot be
 * loaded (unreachable, credentials refused) is a page-level error.
 */
export function useCluster(assetId: number): { state: ClusterState; reload: () => void } {
  const [state, setState] = useState<ClusterState>({ status: "loading" });
  const ctrlRef = useRef<AbortController | null>(null);

  const reload = useCallback(() => {
    ctrlRef.current?.abort();
    const ctrl = new AbortController();
    ctrlRef.current = ctrl;
    setState((s) => (s.status === "ready" ? { ...s, refreshing: true } : { status: "loading" }));
    loadCluster(assetId, ctrl.signal).then(
      (data) => {
        if (!ctrl.signal.aborted) setState({ status: "ready", data, refreshing: false });
      },
      (err) => {
        if (!ctrl.signal.aborted) setState({ status: "error", error: errorMessage(err) });
      }
    );
  }, [assetId]);

  useEffect(() => {
    reload();
    return () => ctrlRef.current?.abort();
  }, [reload]);

  return { state, reload };
}
