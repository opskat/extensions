import { cn, Button } from "@opskat/ui";
import { ChevronRight, Home } from "lucide-react";

interface BreadcrumbNavProps {
  bucket: string;
  prefix: string;
  onNavigate: (prefix: string) => void;
  onNavigateRoot: () => void;
}

export function BreadcrumbNav({ bucket, prefix, onNavigate, onNavigateRoot }: BreadcrumbNavProps) {
  const segments = prefix.split("/").filter(Boolean);

  return (
    <nav className="flex items-center gap-0.5 text-sm min-w-0 overflow-hidden">
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onNavigateRoot}
        className="shrink-0"
      >
        <Home className="h-3.5 w-3.5" />
      </Button>

      {bucket && (
        <span className="flex items-center gap-0.5 min-w-0">
          <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          <button
            onClick={() => onNavigate("")}
            className={cn(
              "truncate max-w-[160px] px-1 py-0.5 rounded text-sm hover:bg-accent",
              segments.length === 0 ? "font-medium text-foreground" : "text-muted-foreground",
            )}
          >
            {bucket}
          </button>
        </span>
      )}

      {segments.map((segment, i) => {
        const path = segments.slice(0, i + 1).join("/") + "/";
        const isLast = i === segments.length - 1;
        return (
          <span key={path} className="flex items-center gap-0.5 min-w-0">
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
            <button
              onClick={() => onNavigate(path)}
              className={cn(
                "truncate max-w-[160px] px-1 py-0.5 rounded text-sm hover:bg-accent",
                isLast ? "font-medium text-foreground" : "text-muted-foreground",
              )}
            >
              {segment}
            </button>
          </span>
        );
      })}
    </nav>
  );
}
