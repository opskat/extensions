import type { SearchHit } from "./types";

// The documents table shows one page of hits as rows: `_id`, then the top-level
// `_source` fields of the hits on that page. The full hit (nested fields and
// metadata included) is what the row detail shows, so the table stays flat.

export const ID_COLUMN = "_id";

/** `_id` followed by every top-level `_source` key on the page, in first-seen order. */
export function documentColumns(hits: SearchHit[]): string[] {
  const keys = new Set<string>();
  for (const hit of hits) {
    for (const key of Object.keys(hit._source ?? {})) keys.add(key);
  }
  return [ID_COLUMN, ...keys];
}

/** One row per hit; an object or array value is shown as its JSON text. */
export function documentRows(hits: SearchHit[]): Record<string, unknown>[] {
  return hits.map((hit) => {
    const row: Record<string, unknown> = { [ID_COLUMN]: hit._id };
    for (const [key, value] of Object.entries(hit._source ?? {})) {
      row[key] = value !== null && typeof value === "object" ? JSON.stringify(value) : value;
    }
    return row;
  });
}
