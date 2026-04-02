import { Button, ScrollArea, Separator } from "@opskat/ui";
import { X, Download, Link } from "lucide-react";
import { useI18n } from "../hooks/useI18n";
import type { PreviewResponse } from "../types";

interface FilePreviewProps {
  data: PreviewResponse;
  loading: boolean;
  onClose: () => void;
  onDownload: (key: string) => void;
  onPresign: (key: string) => void;
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function isImageType(contentType: string): boolean {
  return contentType.startsWith("image/");
}

export function FilePreview({ data, loading, onClose, onDownload, onPresign }: FilePreviewProps) {
  const { t } = useI18n();
  const fileName = data.key.split("/").pop() || data.key;

  return (
    <div className="w-72 shrink-0 border-l border-border flex flex-col">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
        <span className="text-sm font-medium truncate flex-1" title={data.key}>
          {fileName}
        </span>
        <Button variant="ghost" size="icon-xs" onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      {loading ? (
        <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">
          {t("preview.loading")}
        </div>
      ) : (
        <ScrollArea className="flex-1">
          <div className="p-3">
            {/* Metadata */}
            <div className="space-y-1 text-xs text-muted-foreground">
              <div>{t("preview.type")}: {data.contentType}</div>
              <div>{t("preview.size")}: {formatSize(data.size)}</div>
              {data.truncated && (
                <div className="text-warning">{t("preview.truncated")}</div>
              )}
            </div>

            {/* Actions */}
            <div className="flex gap-1 mt-2">
              <Button variant="outline" size="xs" onClick={() => onDownload(data.key)}>
                <Download className="h-3 w-3 mr-1" /> {t("ui.download")}
              </Button>
              <Button variant="outline" size="xs" onClick={() => onPresign(data.key)}>
                <Link className="h-3 w-3 mr-1" /> {t("preview.url")}
              </Button>
            </div>

            <Separator className="my-3" />

            {/* Content preview */}
            {isImageType(data.contentType) && data.encoding === "base64" ? (
              <img
                src={`data:${data.contentType};base64,${data.content}`}
                alt={fileName}
                className="max-w-full rounded border border-border"
              />
            ) : data.encoding === "utf-8" ? (
              <pre className="text-xs whitespace-pre-wrap break-all font-mono bg-muted/50 p-2 rounded max-h-[60vh] overflow-auto">
                {data.content}
              </pre>
            ) : (
              <div className="text-sm text-muted-foreground text-center py-4">
                {t("preview.binaryFile")}
              </div>
            )}
          </div>
        </ScrollArea>
      )}
    </div>
  );
}
