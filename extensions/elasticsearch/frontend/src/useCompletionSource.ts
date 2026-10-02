import { useMemo } from "react";
import { createCompletionSource, parseAliases, type CompletionSource } from "./es/completionSource";
import { esError } from "./es/errors";
import { callTool, esRequest } from "./host";
import type { ClusterData } from "./useCluster";

/**
 * The page's completion data. A new source comes with every (re)load of the
 * cluster, so the refresh button refreshes the aliases and mappings too.
 */
export function useCompletionSource(assetId: number, data: ClusterData | null): CompletionSource {
  return useMemo(
    () =>
      createCompletionSource({
        indices: data?.indices.map((i) => i.index) ?? [],
        loadAliases: async () => {
          const r = await esRequest(assetId, { method: "GET", path: "/_cat/aliases?format=json&h=alias" });
          if (r.status < 200 || r.status >= 300) throw new Error(`HTTP ${r.status}: ${esError(r.body).reason}`);
          return parseAliases(r.body);
        },
        loadMapping: (target) => callTool<unknown>("mapping", { index: target }, assetId),
      }),
    [assetId, data]
  );
}
