import type { IndexInfo } from "./types";

/** System and hidden indices (.kibana, .security, data-stream backing indices …) start with a dot. */
export function isSystemIndex(name: string): boolean {
  return name.startsWith(".");
}

export interface IndexFilter {
  /** Substring of the name; case and surrounding space are ignored. */
  filter: string;
  showSystem: boolean;
}

export function visibleIndices(indices: IndexInfo[], { filter, showSystem }: IndexFilter): IndexInfo[] {
  const needle = filter.trim().toLowerCase();
  return indices.filter(
    (i) => (showSystem || !isSystemIndex(i.index)) && (needle === "" || i.index.toLowerCase().includes(needle))
  );
}
