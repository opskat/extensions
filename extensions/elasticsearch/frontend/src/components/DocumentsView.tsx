import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { AlertCircle, ChevronLeft, ChevronRight, Info, Loader2, Play, SearchX, Table2, X } from "lucide-react";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, cn } from "@opskat/ui";
import { CodeEditor, JsonTreeView, QueryResultTable, type Monaco, type MonacoEditor } from "@opskat/host-ui";
import { bodyContext, suggest } from "../es/completion";
import type { CompletionSource } from "../es/completionSource";
import { documentColumns, documentRows } from "../es/documents";
import { errorMessage } from "../es/errors";
import { formatNumber } from "../es/format";
import { DEFAULT_PAGE_SIZE, PAGE_SIZES, pageRequest, pagination } from "../es/paging";
import { buildSearchRequest, parseSearchResponse, type QueryMode } from "../es/search";
import type { SearchHit } from "../es/types";
import { esRequest } from "../host";
import type { T } from "../i18n";
import { atMost, DSL_EDITOR_FRAME, DSL_LANGUAGE, ES_EDITOR_OPTIONS, setupEsEditor } from "./console/esEditor";

/** The query a search ran with — what paging keeps while the inputs are edited. */
interface Applied {
  mode: QueryMode;
  q: string;
  dsl: string;
  sort: string;
}

type Result =
  | { status: "loading" }
  | { status: "invalid"; reason: string }
  | { status: "esError"; httpStatus: number; type: string; reason: string }
  | { status: "failed"; message: string }
  | {
      status: "ok";
      applied: Applied;
      page: number;
      pageSize: number;
      total: number;
      totalRelation: string;
      took: number;
      hits: SearchHit[];
    };

const EMPTY_QUERY: Applied = { mode: "q", q: "", dsl: "", sort: "" };
const NO_HITS: SearchHit[] = [];
/** Below this width the document detail floats over the table instead of beside it. */
const SIDE_BY_SIDE_MIN_WIDTH = 720;
/** How long completion waits for the index's mapping before showing what it has. */
const COMPLETION_WAIT_MS = 1500;

function isEmptyQuery(a: Applied): boolean {
  return a.mode === "q" ? !a.q.trim() : !a.dsl.trim();
}

/** The index's documents: query string or DSL, sort, a page of hits, the hit detail, paging. */
export function DocumentsView({
  assetId,
  index,
  maxResultWindow,
  completion,
  t,
  lang,
}: {
  assetId: number;
  index: string;
  maxResultWindow: number;
  completion: CompletionSource;
  t: T;
  lang: string;
}) {
  const [mode, setMode] = useState<QueryMode>("q");
  const [q, setQ] = useState("");
  const [dsl, setDsl] = useState("");
  const [sort, setSort] = useState("");
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [result, setResult] = useState<Result>({ status: "loading" });
  const [selected, setSelected] = useState<number | null>(null);

  const ctrlRef = useRef<AbortController | null>(null);
  // Read at request time: the index's window arrives with its settings, after the
  // first search has already started, and must not restart it.
  const windowRef = useRef(maxResultWindow);
  useEffect(() => {
    windowRef.current = maxResultWindow;
  }, [maxResultWindow]);

  const execute = useCallback(
    (applied: Applied, page: number, size: number) => {
      ctrlRef.current?.abort();
      setSelected(null);
      const { from, size: requestSize } = pageRequest(page, size, windowRef.current);
      const req = buildSearchRequest({ index, ...applied, from, size: requestSize });
      if (!req.ok) {
        setResult({ status: "invalid", reason: req.reason });
        return;
      }
      const ctrl = new AbortController();
      ctrlRef.current = ctrl;
      setResult({ status: "loading" });
      esRequest(assetId, req.args, ctrl.signal).then(
        (r) => {
          if (ctrl.signal.aborted) return;
          const parsed = parseSearchResponse(r);
          setResult(
            parsed.ok
              ? { status: "ok", applied, page, pageSize: size, ...parsed }
              : { status: "esError", httpStatus: parsed.status, type: parsed.type, reason: parsed.reason }
          );
        },
        (err) => {
          if (!ctrl.signal.aborted) setResult({ status: "failed", message: errorMessage(err) });
        }
      );
    },
    [assetId, index]
  );

  useEffect(() => {
    execute(EMPTY_QUERY, 0, DEFAULT_PAGE_SIZE);
    return () => ctrlRef.current?.abort();
  }, [execute]);

  const search = useCallback(() => execute({ mode, q, dsl, sort }, 0, pageSize), [execute, mode, q, dsl, sort, pageSize]);
  const searchRef = useRef(search);
  const completionRef = useRef(completion);
  useEffect(() => {
    searchRef.current = search;
    completionRef.current = completion;
  }, [search, completion]);

  const onEditorMount = useCallback(
    (editor: MonacoEditor, monaco: Monaco) => {
      // The same body completion as the console's, for this index's mapping.
      setupEsEditor(editor, monaco, DSL_LANGUAGE, async (value, offset) => {
        const ctx = bodyContext(value, offset, index);
        if (!ctx) return null;
        const source = completionRef.current;
        await atMost(source.ensure({ target: index }), COMPLETION_WAIT_MS);
        return { from: ctx.from, items: suggest(ctx, source.data) };
      });
      editor.addAction({
        id: "es.docs.search",
        label: t("page.docs.search"),
        keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter],
        run: () => searchRef.current(),
      });
    },
    [t, index]
  );

  const onEnter = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") search();
  };

  const loading = result.status === "loading";
  const hits = result.status === "ok" ? result.hits : NO_HITS;
  const columns = useMemo(() => documentColumns(hits), [hits]);
  const rows = useMemo(() => documentRows(hits), [hits]);
  const detail = selected !== null ? hits[selected] : undefined;

  // Wide: the detail sits beside the table; narrow: it floats over it.
  const areaRef = useRef<HTMLDivElement>(null);
  const [wide, setWide] = useState(true);
  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => setWide(entry.contentRect.width >= SIDE_BY_SIDE_MIN_WIDTH));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-start gap-2 border-b border-border bg-muted/20 px-3 py-1.5">
        <div className="flex h-7 shrink-0 overflow-hidden rounded-md border border-border text-[11px]" role="group">
          {(["q", "dsl"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => setMode(m)}
              className={cn(
                "px-2 outline-none first:border-r first:border-border focus-visible:ring-1 focus-visible:ring-ring/45",
                mode === m ? "bg-background font-medium text-foreground" : "text-muted-foreground hover:text-foreground"
              )}
            >
              {m === "q" ? t("page.docs.queryString") : t("page.docs.dsl")}
            </button>
          ))}
        </div>
        {mode === "q" ? (
          <Input
            className="h-7 min-w-0 flex-1 font-mono text-xs"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onEnter}
            placeholder={t("page.docs.queryStringPlaceholder")}
            aria-label={t("page.docs.queryString")}
          />
        ) : (
          <div
            className="min-w-0 flex-1 overflow-hidden rounded-md border border-border bg-background"
            style={DSL_EDITOR_FRAME}
          >
            <CodeEditor
              language="json"
              height={DSL_EDITOR_FRAME.height}
              fontSize={12}
              value={dsl}
              onChange={setDsl}
              onMount={onEditorMount}
              options={ES_EDITOR_OPTIONS}
              placeholder={t("page.docs.dslPlaceholder")}
            />
          </div>
        )}
        <Input
          className="h-7 w-44 shrink-0 font-mono text-xs"
          value={sort}
          onChange={(e) => setSort(e.target.value)}
          onKeyDown={onEnter}
          placeholder={t("page.docs.sortPlaceholder")}
          aria-label={t("page.docs.sort")}
        />
        <Button variant="outline" size="sm" className="h-7 shrink-0 gap-1 text-xs" onClick={search} disabled={loading}>
          {loading ? <Loader2 className="animate-spin" aria-hidden /> : <Play aria-hidden />}
          {t("page.docs.search")}
        </Button>
      </div>

      <div ref={areaRef} className="relative flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <ResultArea result={result} columns={columns} rows={rows} onSelect={setSelected} t={t} />
        </div>
        {detail && (
          <aside
            aria-label={t("page.docs.detail")}
            className={cn(
              "flex flex-col border-l border-border bg-background",
              wide ? "w-[360px] shrink-0" : "absolute inset-y-0 right-0 z-10 w-[min(360px,90%)] shadow-md"
            )}
          >
            <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1.5 text-xs">
              <span className="font-semibold">{t("page.docs.detail")}</span>
              <span className="min-w-0 truncate font-mono text-muted-foreground select-text" title={detail._id}>
                {detail._id}
              </span>
              <span className="flex-1" />
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => setSelected(null)}
                aria-label={t("page.docs.closeDetail")}
              >
                <X />
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-2 font-mono text-xs select-text">
              <JsonTreeView data={detail} />
            </div>
          </aside>
        )}
      </div>

      <Footer
        result={result}
        pageSize={pageSize}
        maxResultWindow={maxResultWindow}
        onPage={(page) => result.status === "ok" && execute(result.applied, page, result.pageSize)}
        onPageSize={(size) => {
          setPageSize(size);
          if (result.status === "ok") execute(result.applied, 0, size);
        }}
        t={t}
        lang={lang}
      />
    </div>
  );
}

function ResultArea({
  result,
  columns,
  rows,
  onSelect,
  t,
}: {
  result: Result;
  columns: string[];
  rows: Record<string, unknown>[];
  onSelect: (row: number) => void;
  t: T;
}) {
  switch (result.status) {
    case "loading":
      return <QueryResultTable columns={[]} rows={[]} loading />;
    case "invalid":
      return <QueryError title={t("page.docs.dslInvalid")} detail={result.reason} />;
    case "esError":
      return (
        <QueryError
          title={t("page.docs.esError", { status: result.httpStatus })}
          detail={result.type ? `${result.type}: ${result.reason}` : result.reason}
        />
      );
    case "failed":
      return <QueryError title={t("page.docs.failed")} detail={result.message} />;
    case "ok":
      if (result.hits.length === 0) {
        // Nothing matched a search for everything: the index itself is empty.
        const emptyIndex = result.total === 0 && isEmptyQuery(result.applied);
        const Icon = emptyIndex ? Table2 : SearchX;
        return (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <Icon className="size-10 opacity-30" aria-hidden />
            <p className="text-xs">{emptyIndex ? t("page.docs.emptyIndex") : t("page.docs.noHits")}</p>
          </div>
        );
      }
      return (
        <QueryResultTable
          columns={columns}
          rows={rows}
          showRowNumber
          rowNumberOffset={result.page * result.pageSize}
          rowDensity="compact"
          onSelectedCellChange={(cell) => cell && onSelect(cell.rowIdx)}
          onSelectedRowsChange={(idxs) => idxs.length === 1 && onSelect(idxs[0])}
        />
      );
  }
}

function QueryError({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <AlertCircle className="size-6 text-destructive/80" aria-hidden />
      <div className="text-xs font-medium text-destructive">{title}</div>
      <pre className="max-h-[50%] max-w-2xl overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-muted/40 px-3 py-2 text-left font-mono text-[11px] text-muted-foreground select-text">
        {detail}
      </pre>
    </div>
  );
}

function Footer({
  result,
  pageSize,
  maxResultWindow,
  onPage,
  onPageSize,
  t,
  lang,
}: {
  result: Result;
  pageSize: number;
  maxResultWindow: number;
  onPage: (page: number) => void;
  onPageSize: (size: number) => void;
  t: T;
  lang: string;
}) {
  const ok = result.status === "ok" ? result : null;
  const paging = ok && pagination({ page: ok.page, pageSize: ok.pageSize, total: ok.total, maxResultWindow });
  const n = (v: number) => formatNumber(v, lang);
  return (
    <div className="flex h-8 shrink-0 items-center gap-2 border-t border-border px-3 text-[11px] text-muted-foreground">
      {ok && (
        <span className="truncate">
          {t("page.docs.summary", {
            total: `${ok.totalRelation === "gte" ? "≥ " : ""}${n(ok.total)}`,
            took: n(ok.took),
          })}
        </span>
      )}
      <span className="flex-1" />
      {paging && paging.limited && (
        <span
          className="flex min-w-0 items-center gap-1 truncate"
          title={t("page.docs.windowReason", { window: n(maxResultWindow) })}
        >
          <Info className="size-3 shrink-0" aria-hidden />
          <span className="truncate">{t("page.docs.windowLimit", { window: n(maxResultWindow) })}</span>
        </span>
      )}
      <span className="shrink-0">{t("page.docs.pageSize")}</span>
      <Select value={String(pageSize)} onValueChange={(v) => onPageSize(Number(v))} disabled={!ok}>
        <SelectTrigger size="sm" className="h-6 w-16 px-2 text-[11px]" aria-label={t("page.docs.pageSize")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {PAGE_SIZES.map((s) => (
            // Select content renders in a portal outside the page root, where only the host's own classes apply.
            <SelectItem key={s} value={String(s)}>
              {s}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {paging && (
        <span className="shrink-0 font-mono">
          {n(paging.rangeStart)}–{n(paging.rangeEnd)}
        </span>
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        disabled={!paging?.canPrev}
        onClick={() => ok && onPage(ok.page - 1)}
        aria-label={t("page.docs.prevPage")}
      >
        <ChevronLeft />
      </Button>
      <Button
        variant="ghost"
        size="icon-xs"
        disabled={!paging?.canNext}
        onClick={() => ok && onPage(ok.page + 1)}
        aria-label={t("page.docs.nextPage")}
        title={
          paging && paging.limited && !paging.canNext
            ? t("page.docs.windowReason", { window: n(maxResultWindow) })
            : undefined
        }
      >
        <ChevronRight />
      </Button>
    </div>
  );
}
