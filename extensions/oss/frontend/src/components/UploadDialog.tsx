import { useState, useCallback, useEffect } from "react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Input,
  Label,
} from "@opskat/ui";
import { Upload, Loader2 } from "lucide-react";
import { useOSSClient } from "../hooks/useOSSClient";
import { useI18n } from "../hooks/useI18n";
import type { TransferProgress } from "../types";

interface UploadDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  bucket: string;
  currentPrefix: string;
  onComplete: () => void;
}

export function UploadDialog({ open, onOpenChange, bucket, currentPrefix, onComplete }: UploadDialogProps) {
  const client = useOSSClient();
  const { t } = useI18n();
  const [objectKey, setObjectKey] = useState("");
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState<TransferProgress | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setError(null);
      setProgress(null);
      setObjectKey("");
    }
  }, [open]);

  const handleUpload = useCallback(async () => {
    if (!objectKey.trim()) return;
    setUploading(true);
    setError(null);
    setProgress(null);

    const fullKey = currentPrefix + objectKey.trim();
    try {
      await client.upload({ bucket, key: fullKey }, (event) => {
        if (event.eventType === "progress") {
          setProgress(event.data as TransferProgress);
        }
      });
      setObjectKey("");
      setProgress(null);
      onComplete();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setUploading(false);
    }
  }, [client, bucket, objectKey, currentPrefix, onComplete]);

  const progressPercent =
    progress && progress.total > 0 ? Math.round((progress.loaded / progress.total) * 100) : 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("upload.title")}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label>{t("upload.objectKey")}</Label>
            <div className="flex items-center gap-1">
              {currentPrefix && (
                <span className="text-sm text-muted-foreground shrink-0">{currentPrefix}</span>
              )}
              <Input
                value={objectKey}
                onChange={(e) => setObjectKey(e.target.value)}
                placeholder={t("upload.objectKeyPlaceholder")}
                disabled={uploading}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {t("upload.filePickerHint")}
            </p>
          </div>

          {progress && (
            <div className="space-y-1">
              <div className="w-full bg-muted rounded-full h-2">
                <div
                  className="bg-primary h-2 rounded-full transition-all"
                  style={{ width: `${progressPercent}%` }}
                />
              </div>
              <p className="text-xs text-muted-foreground text-center">{progressPercent}%</p>
            </div>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={uploading}>
            {t("ui.cancel")}
          </Button>
          <Button onClick={handleUpload} disabled={uploading || !objectKey.trim()}>
            {uploading ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Upload className="h-4 w-4 mr-2" />}
            {t("ui.upload")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
