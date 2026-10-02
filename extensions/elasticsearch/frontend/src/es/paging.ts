// Paging through search hits with from / size. Elasticsearch refuses a search
// whose from + size exceeds the index's `index.max_result_window` (10000 unless
// the index sets it), so pages past the window cannot be reached this way — the
// page shows them as unreachable and says why instead of letting ES reject them.

export const PAGE_SIZES = [20, 50, 100] as const;
export const DEFAULT_PAGE_SIZE = 50;
export const DEFAULT_MAX_RESULT_WINDOW = 10000;

/** The from / size to fetch `page` (0-based) with; the page is cut at the window. */
export function pageRequest(page: number, pageSize: number, maxResultWindow: number): { from: number; size: number } {
  const from = page * pageSize;
  return { from, size: Math.min(pageSize, maxResultWindow - from) };
}

export interface PagingInput {
  page: number;
  pageSize: number;
  total: number;
  maxResultWindow: number;
}

export interface Paging {
  /** Pages the hits fill. */
  pageCount: number;
  /** Pages that start inside the result window. */
  reachablePageCount: number;
  /** Some hits lie past the result window. */
  limited: boolean;
  canPrev: boolean;
  canNext: boolean;
  /** 1-based position of the page's first and last hit; 0 / 0 with no hits. */
  rangeStart: number;
  rangeEnd: number;
}

export function pagination({ page, pageSize, total, maxResultWindow }: PagingInput): Paging {
  const pageCount = Math.ceil(total / pageSize);
  const reachablePageCount = Math.min(pageCount, Math.ceil(maxResultWindow / pageSize));
  const { from, size } = pageRequest(page, pageSize, maxResultWindow);
  const rangeEnd = Math.min(total, from + size);
  return {
    pageCount,
    reachablePageCount,
    limited: total > maxResultWindow,
    canPrev: page > 0,
    canNext: page + 1 < reachablePageCount,
    rangeStart: rangeEnd > from ? from + 1 : 0,
    rangeEnd: rangeEnd > from ? rangeEnd : 0,
  };
}
