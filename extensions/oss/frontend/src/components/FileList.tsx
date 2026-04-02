import {
  cn,
  Button,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@opskat/ui";
import {
  Folder,
  File,
  FileText,
  FileImage,
  FileCode,
  FileArchive,
  FileVideo,
  FileAudio,
  FileJson,
  FileCog,
  FileSpreadsheet,
  FileTerminal,
  FileType,
  FileKey,
  Database,
  Download,
  Trash2,
  Link,
  CheckSquare,
  Square,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useI18n } from "../hooks/useI18n";
import type { FileEntry, ViewMode } from "../types";

interface FileListProps {
  entries: FileEntry[];
  selectedKeys: Set<string>;
  viewMode: ViewMode;
  loading: boolean;
  isTruncated: boolean;
  searchQuery: string;
  onToggleSelect: (key: string) => void;
  onSelectAll: () => void;
  onEntryOpen: (entry: FileEntry) => void;
  onDownload: (key: string) => void;
  onDelete: (keys: string[]) => void;
  onPresign: (key: string) => void;
  onLoadMore: () => void;
}

interface FileIconInfo {
  icon: LucideIcon;
  color: string;
}

const extIconMap: Record<string, FileIconInfo> = {};
function register(exts: string[], icon: LucideIcon, color: string) {
  for (const e of exts) extIconMap[e] = { icon, color };
}
register(["jpg", "jpeg", "png", "gif", "svg", "webp", "ico", "bmp", "tiff", "avif"], FileImage, "text-green-500");
register(["mp4", "avi", "mov", "mkv", "webm", "flv", "wmv", "m4v"], FileVideo, "text-purple-500");
register(["mp3", "wav", "flac", "aac", "ogg", "wma", "m4a", "opus"], FileAudio, "text-pink-500");
register(["js", "ts", "tsx", "jsx", "py", "go", "rs", "java", "c", "cpp", "h", "hpp", "css", "scss", "less", "html", "xml", "vue", "svelte", "rb", "php", "kt", "swift", "dart"], FileCode, "text-blue-500");
register(["yaml", "yml", "toml", "ini", "env", "conf", "cfg", "properties"], FileCog, "text-orange-500");
register(["json", "jsonl", "json5"], FileJson, "text-yellow-600");
register(["zip", "tar", "gz", "bz2", "xz", "rar", "7z", "zst", "lz4"], FileArchive, "text-amber-600");
register(["txt", "md", "log", "csv", "doc", "docx", "rtf"], FileText, "text-sky-500");
register(["xls", "xlsx", "ods"], FileSpreadsheet, "text-emerald-500");
register(["pdf"], FileText, "text-red-500");
register(["sh", "bash", "zsh", "bat", "cmd", "ps1", "fish"], FileTerminal, "text-slate-500");
register(["ttf", "otf", "woff", "woff2", "eot"], FileType, "text-indigo-500");
register(["pem", "key", "cert", "crt", "p12", "pfx", "csr"], FileKey, "text-rose-500");
register(["sql", "db", "sqlite", "sqlite3"], Database, "text-teal-500");

const folderIcon: FileIconInfo = { icon: Folder, color: "text-primary" };
const defaultIcon: FileIconInfo = { icon: File, color: "text-muted-foreground" };

function getFileIcon(name: string, isDir: boolean): FileIconInfo {
  if (isDir) return folderIcon;
  const ext = name.split(".").pop()?.toLowerCase() || "";
  return extIconMap[ext] ?? defaultIcon;
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

function FileRow({
  entry,
  selected,
  onToggleSelect,
  onEntryOpen,
  onDownload,
  onDelete,
  onPresign,
  t,
}: {
  entry: FileEntry;
  selected: boolean;
  onToggleSelect: () => void;
  onEntryOpen: () => void;
  onDownload: () => void;
  onDelete: () => void;
  onPresign: () => void;
  t: (key: string) => string;
}) {
  const { icon: Icon, color: iconColor } = getFileIcon(entry.name, entry.isDir);

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          className={cn(
            "group flex items-center gap-2 px-2 py-1 rounded cursor-pointer",
            "hover:bg-accent/60 transition-colors",
            selected && "bg-primary/10 hover:bg-primary/15",
          )}
          onClick={(e) => {
            if (e.ctrlKey || e.metaKey) {
              onToggleSelect();
            } else {
              onEntryOpen();
            }
          }}
          onDoubleClick={onEntryOpen}
        >
          {/* Checkbox */}
          {!entry.isDir && (
            <button
              onClick={(e) => {
                e.stopPropagation();
                onToggleSelect();
              }}
              className="shrink-0 text-muted-foreground hover:text-foreground"
            >
              {selected ? (
                <CheckSquare className="h-3.5 w-3.5 text-primary" />
              ) : (
                <Square className="h-3.5 w-3.5 opacity-0 group-hover:opacity-100" />
              )}
            </button>
          )}
          {entry.isDir && <div className="w-3.5 shrink-0" />}

          <Icon className={cn("h-4 w-4 shrink-0", iconColor)} />
          <span className="truncate flex-1 text-sm">{entry.name}</span>
          <span className="text-xs text-muted-foreground shrink-0 w-16 text-right">
            {entry.isDir ? "" : formatSize(entry.size)}
          </span>
          <span className="text-xs text-muted-foreground shrink-0 w-24 text-right hidden md:block">
            {formatDate(entry.lastModified)}
          </span>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        {entry.isDir ? (
          <ContextMenuItem onSelect={onEntryOpen}>
            <Folder className="h-4 w-4 mr-2" /> {t("file.open")}
          </ContextMenuItem>
        ) : (
          <>
            <ContextMenuItem onSelect={onEntryOpen}>
              <FileText className="h-4 w-4 mr-2" /> {t("file.preview")}
            </ContextMenuItem>
            <ContextMenuItem onSelect={onDownload}>
              <Download className="h-4 w-4 mr-2" /> {t("ui.download")}
            </ContextMenuItem>
            <ContextMenuItem onSelect={onPresign}>
              <Link className="h-4 w-4 mr-2" /> {t("file.presignedUrl")}
            </ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem onSelect={onDelete} className="text-destructive">
              <Trash2 className="h-4 w-4 mr-2" /> {t("ui.delete")}
            </ContextMenuItem>
          </>
        )}
      </ContextMenuContent>
    </ContextMenu>
  );
}

export function FileList({
  entries,
  selectedKeys,
  viewMode,
  loading,
  isTruncated,
  searchQuery,
  onToggleSelect,
  onSelectAll,
  onEntryOpen,
  onDownload,
  onDelete,
  onPresign,
  onLoadMore,
}: FileListProps) {
  const { t } = useI18n();

  if (loading && entries.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-muted-foreground text-sm">
        {t("ui.loading")}
      </div>
    );
  }

  if (entries.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-muted-foreground text-sm">
        {searchQuery ? t("browser.noResults") : t("browser.emptyDirectory")}
      </div>
    );
  }

  if (viewMode === "grid") {
    return (
      <div className="p-2">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-2">
          {entries.map((entry) => {
            const { icon: Icon, color: iconColor } = getFileIcon(entry.name, entry.isDir);
            const selected = selectedKeys.has(entry.key);
            return (
              <ContextMenu key={entry.key}>
                <ContextMenuTrigger asChild>
                  <button
                    className={cn(
                      "flex flex-col items-center gap-1 p-3 rounded-lg hover:bg-accent/60 transition-colors text-center",
                      selected && "bg-primary/10 ring-1 ring-primary/30",
                    )}
                    onClick={(e) => {
                      if ((e.ctrlKey || e.metaKey) && !entry.isDir) onToggleSelect(entry.key);
                      else onEntryOpen(entry);
                    }}
                    onDoubleClick={() => onEntryOpen(entry)}
                  >
                    <Icon className={cn("h-8 w-8", iconColor)} />
                    <span className="text-xs truncate max-w-full">{entry.name}</span>
                  </button>
                </ContextMenuTrigger>
                <ContextMenuContent>
                  {entry.isDir ? (
                    <ContextMenuItem onSelect={() => onEntryOpen(entry)}>
                      <Folder className="h-4 w-4 mr-2" /> {t("file.open")}
                    </ContextMenuItem>
                  ) : (
                    <>
                      <ContextMenuItem onSelect={() => onEntryOpen(entry)}>
                        <FileText className="h-4 w-4 mr-2" /> {t("file.preview")}
                      </ContextMenuItem>
                      <ContextMenuItem onSelect={() => onDownload(entry.key)}>
                        <Download className="h-4 w-4 mr-2" /> {t("ui.download")}
                      </ContextMenuItem>
                      <ContextMenuItem onSelect={() => onPresign(entry.key)}>
                        <Link className="h-4 w-4 mr-2" /> {t("file.presignedUrl")}
                      </ContextMenuItem>
                      <ContextMenuSeparator />
                      <ContextMenuItem
                        onSelect={() => onDelete([entry.key])}
                        className="text-destructive"
                      >
                        <Trash2 className="h-4 w-4 mr-2" /> {t("ui.delete")}
                      </ContextMenuItem>
                    </>
                  )}
                </ContextMenuContent>
              </ContextMenu>
            );
          })}
        </div>
        {isTruncated && (
          <Button variant="ghost" size="sm" className="w-full mt-2" onClick={onLoadMore}>
            {t("ui.loadMore")}
          </Button>
        )}
      </div>
    );
  }

  // List view
  return (
    <div className="p-1">
      {/* Header */}
      <div className="flex items-center gap-2 px-2 py-1 text-xs text-muted-foreground border-b border-border">
        <button className="shrink-0 w-3.5" onClick={onSelectAll}>
          {selectedKeys.size > 0 ? (
            <CheckSquare className="h-3.5 w-3.5 text-primary" />
          ) : (
            <Square className="h-3.5 w-3.5" />
          )}
        </button>
        <div className="w-4 shrink-0" />
        <span className="flex-1">{t("file.name")}</span>
        <span className="w-16 text-right">{t("file.size")}</span>
        <span className="w-24 text-right hidden md:block">{t("file.modified")}</span>
      </div>

      {entries.map((entry) => (
        <FileRow
          key={entry.key}
          entry={entry}
          selected={selectedKeys.has(entry.key)}
          onToggleSelect={() => onToggleSelect(entry.key)}
          onEntryOpen={() => onEntryOpen(entry)}
          onDownload={() => onDownload(entry.key)}
          onDelete={() => onDelete([entry.key])}
          onPresign={() => onPresign(entry.key)}
          t={t}
        />
      ))}

      {isTruncated && (
        <Button variant="ghost" size="sm" className="w-full mt-2" onClick={onLoadMore}>
          Load more...
        </Button>
      )}
    </div>
  );
}
