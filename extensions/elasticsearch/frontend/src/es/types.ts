// Result shapes of the extension's own tools (tools.go), as the page receives them.

/** `health` */
export interface ClusterHealth {
  clusterName: string;
  version: string;
  status: string;
  numberOfNodes: number;
  numberOfDataNodes: number;
  activeShards: number;
  activePrimaryShards: number;
  unassignedShards: number;
}

/** One row of `indices`. Counts are null for a closed index. */
export interface IndexInfo {
  index: string;
  health: string;
  status: string;
  docsCount: number | null;
  storeSize: number | null;
}

/** `request`: ES's answer as-is — the body parsed when it is JSON, text otherwise. */
export interface RequestResult {
  status: number;
  body: unknown;
}

/** One hit of a `_search` response. */
export interface SearchHit {
  _index: string;
  _id: string;
  _source?: Record<string, unknown>;
  [meta: string]: unknown;
}
