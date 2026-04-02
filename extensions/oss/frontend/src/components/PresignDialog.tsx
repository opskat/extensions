import { useState, useEffect, useCallback } from "react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import { Copy, Check } from "lucide-react";
import { useOSSClient } from "../hooks/useOSSClient";
import { useI18n } from "../hooks/useI18n";

interface PresignDialogProps {
  bucket: string;
  objectKey: string | null;
  onClose: () => void;
}

export function PresignDialog({ bucket, objectKey, onClose }: PresignDialogProps) {
  const client = useOSSClient();
  const { t } = useI18n();
  const [expires, setExpires] = useState("3600");
  const [url, setUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const expiryOptions = [
    { label: t("presign.1hour"), value: "3600" },
    { label: t("presign.6hours"), value: "21600" },
    { label: t("presign.24hours"), value: "86400" },
    { label: t("presign.7days"), value: "604800" },
  ];

  const generateUrl = useCallback(async () => {
    if (!objectKey) return;
    setLoading(true);
    setError(null);
    try {
      const res = await client.presign({ bucket, key: objectKey, expires: Number(expires) });
      setUrl(res.url);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [client, bucket, objectKey, expires]);

  useEffect(() => {
    if (objectKey) {
      setUrl(null);
      setCopied(false);
      setError(null);
      generateUrl();
    }
  }, [objectKey, generateUrl]);

  const handleCopy = useCallback(async () => {
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback: select the input text
    }
  }, [url]);

  return (
    <Dialog open={objectKey !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("presign.title")}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="text-sm text-muted-foreground truncate" title={objectKey || ""}>
            {objectKey}
          </div>

          <div className="space-y-2">
            <Label>{t("presign.expiration")}</Label>
            <div className="flex items-center gap-2">
              <Select value={expires} onValueChange={setExpires}>
                <SelectTrigger className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {expiryOptions.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button variant="outline" size="sm" onClick={generateUrl} disabled={loading}>
                {t("presign.regenerate")}
              </Button>
            </div>
          </div>

          {url && (
            <div className="space-y-2">
              <Label>{t("presign.url")}</Label>
              <div className="flex gap-2">
                <Input value={url} readOnly className="text-xs font-mono" />
                <Button variant="outline" size="icon" onClick={handleCopy}>
                  {copied ? <Check className="h-4 w-4 text-success" /> : <Copy className="h-4 w-4" />}
                </Button>
              </div>
            </div>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("ui.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
