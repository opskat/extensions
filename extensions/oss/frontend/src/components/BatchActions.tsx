import { useState, useCallback } from "react";
import { Button, ConfirmDialog } from "@opskat/ui";
import { Trash2, X } from "lucide-react";
import { useOSSClient } from "../hooks/useOSSClient";
import { useI18n } from "../hooks/useI18n";
import type { BatchProgress } from "../types";

interface BatchActionsProps {
  bucket: string;
  selectedKeys: Set<string>;
  currentPrefix: string;
  onClearSelection: () => void;
  onComplete: () => void;
  onError: (msg: string) => void;
}

export function BatchActions({
  bucket,
  selectedKeys,
  currentPrefix: _currentPrefix,
  onClearSelection,
  onComplete,
  onError,
}: BatchActionsProps) {
  const client = useOSSClient();
  const { t } = useI18n();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [operating, setOperating] = useState(false);
  const [progress, setProgress] = useState<BatchProgress | null>(null);

  const keys = Array.from(selectedKeys);
  const count = keys.length;

  const handleDelete = useCallback(async () => {
    setDeleteOpen(false);
    setOperating(true);
    setProgress(null);
    try {
      const res = await client.batchDelete({ bucket, keys }, (event) => {
        if (event.eventType === "progress") setProgress(event.data as BatchProgress);
      });
      if (res.errorCount && res.errorCount > 0) {
        onError(t("batch.deleteResult", { deleted: res.deletedCount, failed: res.errorCount }));
      }
      onClearSelection();
      onComplete();
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setOperating(false);
      setProgress(null);
    }
  }, [client, bucket, keys, onClearSelection, onComplete, onError, t]);

  return (
    <>
      <div className="flex items-center gap-2 px-3 py-1.5 bg-accent/50 border-b border-border">
        <span className="text-sm text-muted-foreground">
          {t("batch.selected", { count })}
          {operating && progress && ` (${progress.done}/${progress.total})`}
        </span>

        <div className="flex-1" />

        <Button
          variant="ghost"
          size="xs"
          onClick={() => setDeleteOpen(true)}
          disabled={operating}
          className="text-destructive hover:text-destructive"
        >
          <Trash2 className="h-3.5 w-3.5 mr-1" /> {t("ui.delete")}
        </Button>

        <Button variant="ghost" size="xs" onClick={onClearSelection} disabled={operating}>
          <X className="h-3.5 w-3.5 mr-1" /> {t("ui.clear")}
        </Button>
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("batch.deleteConfirmTitle", { count })}
        description={t("batch.deleteConfirmDescription")}
        confirmText={t("ui.delete")}
        onConfirm={handleDelete}
        variant="destructive"
      />
    </>
  );
}
