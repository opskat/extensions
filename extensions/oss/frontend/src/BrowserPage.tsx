import { useState, useEffect, useCallback } from "react";
import { cn, Button, ScrollArea, Separator, useResizeHandle } from "@opskat/ui";
import {
  RefreshCw,
  PanelLeftClose,
  PanelLeftOpen,
  LayoutList,
  LayoutGrid,
  Upload,
  HardDrive,
} from "lucide-react";
import { useOSSClient } from "./hooks/useOSSClient";
import { useI18n } from "./hooks/useI18n";
import { BreadcrumbNav } from "./components/BreadcrumbNav";
import { FileList } from "./components/FileList";
import { FileTree } from "./components/FileTree";
import { SearchBar } from "./components/SearchBar";
import { FilePreview } from "./components/FilePreview";
import { UploadDialog } from "./components/UploadDialog";
import { PresignDialog } from "./components/PresignDialog";
import { BatchActions } from "./components/BatchActions";
import type {
  BucketInfo,
  FileEntry,
  ViewMode,
  BrowseResponse,
  PreviewResponse,
} from "./types";

// Helper: convert browse response into unified FileEntry list
function toFileEntries(res: BrowseResponse): FileEntry[] {
  const dirs: FileEntry[] = (res.commonPrefixes || []).map((p) => {
    const name = p.replace(res.prefix, "").replace(/\/$/, "") + "/";
    return { key: p, name, isDir: true, size: 0 };
  });
  const files: FileEntry[] = (res.objects || [])
    .filter((o) => o.key !== res.prefix) // exclude the "directory" object itself
    .map((o) => {
      const name = o.key.replace(res.prefix, "");
      return { key: o.key, name, isDir: false, size: o.size, lastModified: o.lastModified };
    });
  return [...dirs, ...files];
}

interface BrowserPageProps {
  assetId?: number;
}

export function BrowserPage({ assetId }: BrowserPageProps) {
  const client = useOSSClient(assetId);
  const { t } = useI18n();

  // -- Bucket state --
  const [currentBucket, setCurrentBucket] = useState("");
  const [buckets, setBuckets] = useState<BucketInfo[] | null>(null);
  const [loadingBuckets, setLoadingBuckets] = useState(true);

  // -- File browser state --
  const [prefix, setPrefix] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isTruncated, setIsTruncated] = useState(false);
  const [continuationToken, setContinuationToken] = useState<string | undefined>();

  // Selection
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set());

  // View
  const [viewMode, setViewMode] = useState<ViewMode>("list");
  const [sidebarOpen, setSidebarOpen] = useState(true);

  // Preview
  const [previewData, setPreviewData] = useState<PreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  // Search
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<FileEntry[] | null>(null);

  // Sidebar resize
  const { width: sidebarWidth, isResizing: sidebarResizing, handleMouseDown: handleSidebarResizeStart } = useResizeHandle({
    defaultWidth: 224,
    minWidth: 160,
    maxWidth: 480,
    storageKey: "oss_sidebar_width",
  });

  // Upload dialog
  const [uploadOpen, setUploadOpen] = useState(false);

  // Presign dialog
  const [presignKey, setPresignKey] = useState<string | null>(null);

  // -- Load buckets on mount --
  const fetchBuckets = useCallback(async () => {
    setLoadingBuckets(true);
    setError(null);
    try {
      const res = await client.listBuckets({} as never);
      setBuckets(res.buckets);
      // Auto-select default bucket if configured
      if (res.defaultBucket) {
        setCurrentBucket(res.defaultBucket);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoadingBuckets(false);
    }
  }, [client]);

  useEffect(() => {
    fetchBuckets();
  }, [fetchBuckets]);

  // -- Data fetching --
  const fetchEntries = useCallback(
    async (newPrefix: string, token?: string) => {
      if (!currentBucket) return;
      setLoading(true);
      setError(null);
      try {
        const res = await client.browse({
          bucket: currentBucket,
          prefix: newPrefix,
          continuationToken: token,
        });
        const newEntries = toFileEntries(res);
        if (token) {
          setEntries((prev) => [...prev, ...newEntries]);
        } else {
          setEntries(newEntries);
        }
        setIsTruncated(res.isTruncated);
        setContinuationToken(res.nextContinuationToken);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [client, currentBucket],
  );

  // Reload when bucket or prefix changes
  useEffect(() => {
    if (!currentBucket) return;
    setSelectedKeys(new Set());
    setPreviewData(null);
    setSearchResults(null);
    setSearchQuery("");
    fetchEntries(prefix);
  }, [currentBucket, prefix, fetchEntries]);

  // -- Bucket navigation --
  const selectBucket = useCallback((name: string) => {
    setCurrentBucket(name);
    setPrefix("");
    setEntries([]);
  }, []);

  const navigateToRoot = useCallback(() => {
    setCurrentBucket("");
    setPrefix("");
    setEntries([]);
    setPreviewData(null);
    setSelectedKeys(new Set());
    setSearchResults(null);
    setSearchQuery("");
  }, []);

  // -- File navigation --
  const navigateTo = useCallback((newPrefix: string) => {
    setPrefix(newPrefix);
  }, []);

  const handleEntryOpen = useCallback(
    (entry: FileEntry) => {
      if (entry.isDir) {
        navigateTo(entry.key);
      } else {
        // Preview file
        setPreviewLoading(true);
        client
          .preview({ bucket: currentBucket, key: entry.key })
          .then(setPreviewData)
          .catch(() => setPreviewData(null))
          .finally(() => setPreviewLoading(false));
      }
    },
    [client, currentBucket, navigateTo],
  );

  // -- Selection --
  const toggleSelect = useCallback((key: string) => {
    setSelectedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const displayEntries = searchResults ?? entries;

  const selectAll = useCallback(() => {
    const fileKeys = displayEntries.filter((e) => !e.isDir).map((e) => e.key);
    setSelectedKeys((prev) => (prev.size === fileKeys.length ? new Set() : new Set(fileKeys)));
  }, [displayEntries]);

  // -- Actions --
  const handleRefresh = useCallback(() => {
    fetchEntries(prefix);
  }, [prefix, fetchEntries]);

  const handleLoadMore = useCallback(() => {
    if (continuationToken) fetchEntries(prefix, continuationToken);
  }, [prefix, continuationToken, fetchEntries]);

  const handleDownload = useCallback(
    async (key: string) => {
      try {
        await client.download({ bucket: currentBucket, key });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [client, currentBucket],
  );

  const handleDelete = useCallback(
    async (keys: string[]) => {
      try {
        await client.batchDelete({ bucket: currentBucket, keys });
        setSelectedKeys(new Set());
        fetchEntries(prefix);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [client, currentBucket, prefix, fetchEntries],
  );

  const handleSearch = useCallback(
    async (query: string) => {
      setSearchQuery(query);
      if (!query) {
        setSearchResults(null);
        return;
      }
      try {
        const res = await client.search({ bucket: currentBucket, prefix: prefix + query });
        setSearchResults(
          res.objects.map((o) => ({
            key: o.key,
            name: o.key.replace(prefix, ""),
            isDir: false,
            size: o.size,
            lastModified: o.lastModified,
          })),
        );
      } catch {
        setSearchResults([]);
      }
    },
    [client, currentBucket, prefix],
  );

  const handleUploadComplete = useCallback(() => {
    setUploadOpen(false);
    fetchEntries(prefix);
  }, [prefix, fetchEntries]);

  // ========== Bucket list view ==========
  if (!currentBucket) {
    return (
      <div className="h-full flex flex-col overflow-hidden">
        <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
          <BreadcrumbNav
            bucket=""
            prefix=""
            onNavigate={() => {}}
            onNavigateRoot={() => {}}
          />
          <div className="flex-1" />
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={fetchBuckets}
            disabled={loadingBuckets}
            title={t("ui.refresh")}
          >
            <RefreshCw className={cn("h-4 w-4", loadingBuckets && "animate-spin")} />
          </Button>
        </div>

        {error && (
          <div className="px-3 py-2 bg-destructive/10 text-destructive text-sm border-b border-border">
            {error}
            <button className="ml-2 underline" onClick={() => setError(null)}>
              {t("ui.dismiss")}
            </button>
          </div>
        )}

        <div className="flex-1 overflow-auto p-4">
          {loadingBuckets && (
            <div className="text-muted-foreground text-sm">{t("browser.loadingBuckets")}</div>
          )}
          {!loadingBuckets && buckets && buckets.length === 0 && (
            <div className="text-muted-foreground text-sm">{t("browser.noBucketsFound")}</div>
          )}
          {!loadingBuckets && buckets && buckets.length > 0 && (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-3">
              {buckets.map((b) => (
                <button
                  key={b.name}
                  className="flex items-center gap-3 p-3 rounded-lg border border-border hover:bg-accent/60 transition-colors text-left"
                  onClick={() => selectBucket(b.name)}
                >
                  <HardDrive className="h-5 w-5 text-primary shrink-0" />
                  <div className="min-w-0">
                    <div className="font-medium text-sm truncate">{b.name}</div>
                    {b.creationDate && (
                      <div className="text-xs text-muted-foreground">
                        {new Date(b.creationDate).toLocaleDateString()}
                      </div>
                    )}
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    );
  }

  // ========== File browser view ==========
  return (
    <div className="flex h-full overflow-hidden">
      {/* Left sidebar — FileTree */}
      {sidebarOpen && (
        <div className="relative shrink-0 flex flex-col border-r border-border" style={{ width: sidebarWidth }}>
          <div className="p-2 text-xs font-medium text-muted-foreground uppercase tracking-wider">
            {t("browser.directories")}
          </div>
          <ScrollArea className="flex-1">
            <FileTree
              bucket={currentBucket}
              rootPrefixes={entries.filter((e) => e.isDir).map((e) => e.key)}
              activePrefix={prefix}
              onNavigate={navigateTo}
            />
          </ScrollArea>
          {/* Resize handle */}
          <div
            className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize z-10 hover:bg-primary/20 active:bg-primary/30 transition-colors"
            onMouseDown={handleSidebarResizeStart}
          />
        </div>
      )}
      {/* Overlay during resize to prevent text selection */}
      {sidebarResizing && <div className="fixed inset-0 z-50 cursor-col-resize" />}

      {/* Right main area */}
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Toolbar */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => setSidebarOpen(!sidebarOpen)}
            title={sidebarOpen ? t("browser.hideSidebar") : t("browser.showSidebar")}
          >
            {sidebarOpen ? (
              <PanelLeftClose className="h-4 w-4" />
            ) : (
              <PanelLeftOpen className="h-4 w-4" />
            )}
          </Button>

          <Separator orientation="vertical" className="h-4" />

          <BreadcrumbNav
            bucket={currentBucket}
            prefix={prefix}
            onNavigate={navigateTo}
            onNavigateRoot={navigateToRoot}
          />

          <div className="flex-1" />

          <SearchBar value={searchQuery} onSearch={handleSearch} />

          <Separator orientation="vertical" className="h-4" />

          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => setViewMode(viewMode === "list" ? "grid" : "list")}
            title={viewMode === "list" ? t("browser.gridView") : t("browser.listView")}
          >
            {viewMode === "list" ? (
              <LayoutGrid className="h-4 w-4" />
            ) : (
              <LayoutList className="h-4 w-4" />
            )}
          </Button>

          <Button variant="ghost" size="icon-xs" onClick={() => setUploadOpen(true)} title={t("ui.upload")}>
            <Upload className="h-4 w-4" />
          </Button>

          <Button
            variant="ghost"
            size="icon-xs"
            onClick={handleRefresh}
            disabled={loading}
            title={t("ui.refresh")}
          >
            <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
          </Button>
        </div>

        {selectedKeys.size > 0 && (
          <BatchActions
            bucket={currentBucket}
            selectedKeys={selectedKeys}
            currentPrefix={prefix}
            onClearSelection={() => setSelectedKeys(new Set())}
            onComplete={() => fetchEntries(prefix)}
            onError={(msg) => setError(msg)}
          />
        )}

        {/* Error banner */}
        {error && (
          <div className="px-3 py-2 bg-destructive/10 text-destructive text-sm border-b border-border">
            {error}
            <button className="ml-2 underline" onClick={() => setError(null)}>
              {t("ui.dismiss")}
            </button>
          </div>
        )}

        {/* Content area */}
        <div className="flex-1 flex overflow-hidden">
          <ScrollArea className="flex-1">
            <FileList
              entries={displayEntries}
              selectedKeys={selectedKeys}
              viewMode={viewMode}
              loading={loading}
              isTruncated={isTruncated}
              searchQuery={searchQuery}
              onToggleSelect={toggleSelect}
              onSelectAll={selectAll}
              onEntryOpen={handleEntryOpen}
              onDownload={handleDownload}
              onDelete={handleDelete}
              onPresign={(key) => setPresignKey(key)}
              onLoadMore={handleLoadMore}
            />
          </ScrollArea>

          {previewData && (
            <FilePreview
              data={previewData}
              loading={previewLoading}
              onClose={() => setPreviewData(null)}
              onDownload={handleDownload}
              onPresign={(key) => setPresignKey(key)}
            />
          )}
        </div>
      </div>

      {/* Dialogs */}
      <UploadDialog
        open={uploadOpen}
        onOpenChange={setUploadOpen}
        bucket={currentBucket}
        currentPrefix={prefix}
        onComplete={handleUploadComplete}
      />
      <PresignDialog bucket={currentBucket} objectKey={presignKey} onClose={() => setPresignKey(null)} />
    </div>
  );
}
