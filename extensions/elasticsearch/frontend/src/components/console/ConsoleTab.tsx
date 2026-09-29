import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, Loader2, Play, TerminalSquare } from "lucide-react";
import { Button, cn, useResizeHandle } from "@opskat/ui";
import { CodeEditor, JsonTreeView, type Monaco, type MonacoEditor } from "@opskat/host-ui";
import { consoleContext, suggest } from "../../es/completion";
import type { CompletionSource } from "../../es/completionSource";
import { requestAt, responseBytes, responseText, splitRequests, type ConsoleRequest } from "../../es/console";
import { errorMessage } from "../../es/errors";
import { formatBytes, formatNumber } from "../../es/format";
import type { ConsoleTabModel } from "../../es/tabs";
import { esRequest } from "../../host";
import type { T } from "../../i18n";
import { atMost, CONSOLE_LANGUAGE, ES_EDITOR_OPTIONS, setupEsEditor } from "./esEditor";

/** How long completion waits for aliases / a mapping before showing what it has. */
const COMPLETION_WAIT_MS = 1500;

type Response =
  | { status: "idle" }
  | { status: "noRequest" }
  | { status: "loading"; request: string }
  | { status: "done"; request: string; httpStatus: number; body: unknown; ms: number; bytes: number }
  | { status: "failed"; request: string; message: string };

export interface ConsoleTabProps {
  assetId: number;
  tab: ConsoleTabModel;
  /** The page's completion data: its loaded indices, aliases and mappings on demand. */
  completion: CompletionSource;
  /** Every edit, for the page to save the console (useConsolePersistence). */
  onTextChange: (id: string, text: string) => void;
  t: T;
  lang: string;
}

const RUN_KEYS = /Mac/i.test(navigator.platform) ? "⌘↵" : "Ctrl+↵";

const requestLabel = (r: ConsoleRequest) => `${r.method} ${r.path}`;

/**
 * One console: an editor of `METHOD /path` requests on the left, the response of
 * the last one run on the right. ⌘/Ctrl+Enter or Run sends the request under the
 * cursor as-is — no confirmation, whatever it does (design decision 8).
 */
export function ConsoleTab({ assetId, tab, completion, onTextChange, t, lang }: ConsoleTabProps) {
  const [text, setText] = useState(tab.initialText);
  const [cursorLine, setCursorLine] = useState(0);
  const [response, setResponse] = useState<Response>({ status: "idle" });
  const [view, setView] = useState<"tree" | "raw">("tree");
  const split = useResizeHandle({
    defaultSize: 440,
    minSize: 240,
    maxSize: 1400,
    reverse: true,
    storageKey: "ext-elasticsearch:console-response-width",
  });

  const requests = useMemo(() => splitRequests(text), [text]);
  const current = requestAt(requests, cursorLine);

  const ctrlRef = useRef<AbortController | null>(null);
  useEffect(() => () => ctrlRef.current?.abort(), []);

  const execute = useCallback(
    (req: ConsoleRequest | null) => {
      if (!req) {
        setResponse({ status: "noRequest" });
        return;
      }
      ctrlRef.current?.abort();
      const ctrl = new AbortController();
      ctrlRef.current = ctrl;
      const request = requestLabel(req);
      setResponse({ status: "loading", request });
      const started = performance.now();
      esRequest(assetId, { method: req.method, path: req.path, ...(req.body && { body: req.body }) }, ctrl.signal).then(
        (r) => {
          if (ctrl.signal.aborted) return;
          const ms = Math.round(performance.now() - started);
          setResponse({ status: "done", request, httpStatus: r.status, body: r.body, ms, bytes: responseBytes(r.body) });
        },
        (err) => {
          if (!ctrl.signal.aborted) setResponse({ status: "failed", request, message: errorMessage(err) });
        }
      );
    },
    [assetId]
  );

  // The editor's shortcut and completion outlive renders; they read the latest through refs.
  const executeRef = useRef(execute);
  const completionRef = useRef(completion);
  useEffect(() => {
    executeRef.current = execute;
    completionRef.current = completion;
  }, [execute, completion]);

  const onMount = useCallback(
    (editor: MonacoEditor, monaco: Monaco) => {
      setupEsEditor(editor, monaco, CONSOLE_LANGUAGE, async (value, offset) => {
        const ctx = consoleContext(value, offset);
        if (!ctx) return null;
        const source = completionRef.current;
        await atMost(
          source.ensure({
            aliases: ctx.kind === "path" && ctx.scope === "root",
            target: ctx.kind === "body" ? ctx.target : null,
          }),
          COMPLETION_WAIT_MS
        );
        return { from: ctx.from, items: suggest(ctx, source.data) };
      });
      editor.onDidChangeCursorPosition((e) => setCursorLine(e.position.lineNumber - 1));
      editor.addAction({
        id: "es.console.run",
        label: t("page.console.run"),
        keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter],
        // Straight from the editor: the keystroke may come before React has seen the last edit.
        run: () => {
          const model = editor.getModel();
          const pos = editor.getPosition();
          if (model && pos) executeRef.current(requestAt(splitRequests(model.getValue()), pos.lineNumber - 1));
        },
      });
    },
    [t]
  );

  const onChange = (value: string) => {
    setText(value);
    onTextChange(tab.id, value);
  };

  const loading = response.status === "loading";

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/20 px-3">
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
            {current ? (
              <>
                <span className="mr-1.5">{t("page.console.current")}</span>
                <span className="text-foreground select-text">{requestLabel(current)}</span>
              </>
            ) : (
              t("page.console.noRequest")
            )}
          </span>
          <Button
            size="sm"
            className="h-7 shrink-0 gap-1 text-xs"
            onClick={() => execute(current)}
            disabled={!current}
            title={t("page.console.runHint")}
          >
            {loading ? <Loader2 className="animate-spin" aria-hidden /> : <Play aria-hidden />}
            {t("page.console.run")}
            <kbd className="font-sans opacity-70">{RUN_KEYS}</kbd>
          </Button>
        </div>
        <div className="min-h-0 flex-1">
          <CodeEditor
            language="plaintext"
            height="100%"
            fontSize={12}
            value={text}
            onChange={onChange}
            onMount={onMount}
            options={ES_EDITOR_OPTIONS}
            placeholder={t("page.console.editorPlaceholder")}
          />
        </div>
      </div>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("page.console.resize")}
        onMouseDown={split.handleMouseDown}
        className={cn(
          "w-1 shrink-0 cursor-col-resize border-l border-border transition-colors hover:bg-primary/50",
          split.isResizing && "bg-primary/50"
        )}
      />
      <section
        aria-label={t("page.console.response")}
        className="flex min-w-0 shrink-0 flex-col"
        style={{ width: split.size, maxWidth: "70%" }}
      >
        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/20 px-3 text-[11px]">
          {response.status === "done" && (
            <>
              <span
                className={cn(
                  "rounded px-1.5 py-0.5 font-semibold",
                  response.httpStatus >= 200 && response.httpStatus < 300
                    ? "bg-success/15 text-success"
                    : "bg-destructive/15 text-destructive"
                )}
              >
                {response.httpStatus}
              </span>
              <span className="shrink-0 text-muted-foreground">
                {t("page.console.elapsed", { ms: formatNumber(response.ms, lang) })} · {formatBytes(response.bytes)}
              </span>
            </>
          )}
          {"request" in response && (
            <span className="min-w-0 truncate font-mono text-muted-foreground" title={response.request}>
              {response.request}
            </span>
          )}
          <span className="flex-1" />
          {response.status === "done" && typeof response.body !== "string" && (
            <div className="flex h-6 shrink-0 overflow-hidden rounded-md border border-border" role="group">
              {(["tree", "raw"] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  aria-pressed={view === v}
                  onClick={() => setView(v)}
                  className={cn(
                    "px-2 outline-none first:border-r first:border-border focus-visible:ring-1 focus-visible:ring-ring/45",
                    view === v ? "bg-background font-medium text-foreground" : "text-muted-foreground hover:text-foreground"
                  )}
                >
                  {v === "tree" ? t("page.console.tree") : t("page.console.raw")}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <ResponseBody response={response} view={view} t={t} />
        </div>
      </section>
    </div>
  );
}

function ResponseBody({ response, view, t }: { response: Response; view: "tree" | "raw"; t: T }) {
  switch (response.status) {
    case "idle":
    case "noRequest":
      return (
        <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
          <TerminalSquare className="size-8 opacity-40" aria-hidden />
          <p className="text-xs">{response.status === "idle" ? t("page.console.idle") : t("page.console.noRequest")}</p>
        </div>
      );
    case "loading":
      return (
        <div className="flex h-full items-center justify-center">
          <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
        </div>
      );
    case "failed":
      // No answer came back (unreachable, TLS, timeout): the tool's message as-is.
      return (
        <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
          <AlertCircle className="size-6 text-destructive/80" aria-hidden />
          <div className="text-xs font-medium text-destructive">{t("page.console.failed")}</div>
          <pre className="max-w-full whitespace-pre-wrap break-all rounded-md border border-border bg-muted/40 px-3 py-2 text-left font-mono text-[11px] text-muted-foreground select-text">
            {response.message}
          </pre>
        </div>
      );
    case "done":
      // Any status ES answers with, 4xx / 5xx included, is shown as it came.
      if (response.body === "") {
        return <p className="p-3 text-xs text-muted-foreground">{t("page.console.emptyBody")}</p>;
      }
      if (view === "tree" && typeof response.body !== "string") {
        return (
          <div className="p-3 font-mono text-xs select-text">
            <JsonTreeView data={response.body} />
          </div>
        );
      }
      return (
        <pre className="p-3 font-mono text-xs whitespace-pre-wrap break-all select-text">{responseText(response.body)}</pre>
      );
  }
}
