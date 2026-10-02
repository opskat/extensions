import { AlertCircle, Loader2, RefreshCw } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import type { CSSProperties } from "react";
import type { T } from "../i18n";

export function healthColor(health: string): string {
  switch (health) {
    case "green":
      return "bg-success";
    case "yellow":
      return "bg-warning";
    case "red":
      return "bg-destructive";
    default:
      return "bg-muted-foreground/40";
  }
}

export function healthText(health: string): string {
  switch (health) {
    case "green":
      return "text-success";
    case "yellow":
      return "text-warning";
    case "red":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

/** A health dot; the status is spelled out for screen readers, never color alone. */
export function HealthDot({ health, className }: { health: string; className?: string }) {
  return (
    <span className={cn("inline-block size-2 shrink-0 rounded-full", healthColor(health), className)}>
      <span className="sr-only">{health}</span>
    </span>
  );
}

export function HealthBadge({ health }: { health: string }) {
  const soft = health === "green" ? "bg-success/15" : health === "yellow" ? "bg-warning/15" : "bg-destructive/15";
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1 rounded-full px-2 text-[10px] font-semibold uppercase",
        soft,
        healthText(health)
      )}
    >
      <span className={cn("inline-block size-2 shrink-0 rounded-full", healthColor(health))} aria-hidden />
      {health}
    </span>
  );
}

export function SkeletonBar({ className, style }: { className?: string; style?: CSSProperties }) {
  return <div className={cn("animate-pulse rounded bg-foreground/10", className)} style={style} aria-hidden />;
}

export function Spinner({ className }: { className?: string }) {
  return (
    <div className={cn("flex h-full items-center justify-center", className)}>
      <Loader2 className="size-5 animate-spin text-muted-foreground" />
    </div>
  );
}

/** A failed load inside a pane: the cause, and a retry when one makes sense. */
export function InlineError({
  title,
  detail,
  onRetry,
  t,
}: {
  title: string;
  detail: string;
  onRetry?: () => void;
  t: T;
}) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <AlertCircle className="size-6 text-destructive/80" aria-hidden />
      <div className="text-xs font-medium">{title}</div>
      <pre className="max-w-xl whitespace-pre-wrap break-all rounded-md border border-border bg-muted/40 px-3 py-2 text-left font-mono text-[11px] text-muted-foreground select-text">
        {detail}
      </pre>
      {onRetry && (
        <Button variant="outline" size="xs" onClick={onRetry}>
          <RefreshCw aria-hidden />
          {t("page.action.retry")}
        </Button>
      )}
    </div>
  );
}
