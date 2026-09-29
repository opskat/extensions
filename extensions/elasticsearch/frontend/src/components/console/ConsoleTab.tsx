import { Code2 } from "lucide-react";
import type { ConsoleTabModel } from "../../es/tabs";
import type { IndexInfo } from "../../es/types";
import type { T } from "../../i18n";

/**
 * Console tab content — a placeholder, and the seam the console slots into.
 *
 * ElasticsearchPage renders one ConsoleTab per `kind: "console"` entry of the
 * tab model (es/tabs.ts), keyed by the tab's id and kept mounted while another
 * tab is shown (hidden, not unmounted), so an editor keeps its text and response
 * across tab switches. The console replaces this component's body and keeps its
 * props: `assetId` scopes its `request` tool calls (host.ts esRequest), `tab`
 * names the console (id / number; persisting console tabs per asset extends the
 * tab model, which is plain serialisable data), `indices` is the page's loaded
 * index list for path completion, `t` / `lang` the page's i18n.
 */
export interface ConsoleTabProps {
  assetId: number;
  tab: ConsoleTabModel;
  indices: IndexInfo[];
  t: T;
  lang: string;
}

export function ConsoleTab({ t }: ConsoleTabProps) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="flex max-w-sm flex-col items-center gap-2 rounded-lg border border-dashed border-border px-6 py-8 text-center text-muted-foreground">
        <Code2 className="size-8 opacity-40" aria-hidden />
        <p className="text-xs">{t("page.console.placeholder")}</p>
      </div>
    </div>
  );
}
