import { DEFAULT_MAX_RESULT_WINDOW } from "./paging";

export interface IndexSettings {
  primaries: number;
  replicas: number;
  /** `index.max_result_window`: how far from + size may reach. */
  maxResultWindow: number;
}

/** Reads what the index tab needs from a `GET /<index>/_settings` body. */
export function parseIndexSettings(body: unknown, index: string): IndexSettings {
  const entry = (body as Record<string, { settings?: { index?: Record<string, string> } }> | null)?.[index];
  const s = entry?.settings?.index;
  if (!s) throw new Error(`no settings for index ${index} in the response`);
  return {
    primaries: Number(s.number_of_shards),
    replicas: Number(s.number_of_replicas),
    maxResultWindow: s.max_result_window ? Number(s.max_result_window) : DEFAULT_MAX_RESULT_WINDOW,
  };
}
