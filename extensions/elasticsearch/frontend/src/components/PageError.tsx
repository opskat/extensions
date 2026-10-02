import { AlertCircle, RefreshCw } from "lucide-react";
import { Button } from "@opskat/ui";
import { loadErrorKind } from "../es/errors";
import type { T } from "../i18n";

/** The whole page when the cluster cannot be loaded: why, and a retry. */
export function PageError({ error, onRetry, t }: { error: string; onRetry: () => void; t: T }) {
  const kind = loadErrorKind(error);
  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-3 bg-background px-6 text-center text-foreground">
      <AlertCircle className="size-10 text-destructive/70" aria-hidden />
      <div className="text-sm font-semibold">{t(`page.error.${kind}.title`)}</div>
      <div className="max-w-md text-xs text-muted-foreground">{t(`page.error.${kind}.hint`)}</div>
      <pre className="max-w-xl whitespace-pre-wrap break-all rounded-md border border-border bg-muted/40 px-3 py-2 text-left font-mono text-[11px] text-muted-foreground select-text">
        {error}
      </pre>
      <Button variant="outline" size="sm" className="h-8 gap-1 text-xs" onClick={onRetry}>
        <RefreshCw aria-hidden />
        {t("page.action.retry")}
      </Button>
    </div>
  );
}
