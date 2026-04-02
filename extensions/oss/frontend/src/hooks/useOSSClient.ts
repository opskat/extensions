import { useCallback, useMemo } from "react";
import type {
  BrowseParams,
  BrowseResponse,
  ListBucketsResponse,
  SearchParams,
  SearchResponse,
  PreviewParams,
  PreviewResponse,
  UploadParams,
  UploadResponse,
  DownloadParams,
  DownloadResponse,
  BatchDeleteParams,
  BatchDeleteResponse,
  BatchCopyParams,
  BatchCopyResponse,
  PresignParams,
  PresignResponse,
} from "../types";

type EventCallback = (event: { eventType: string; data: unknown }) => void;

function getAPI() {
  const api = window.__OPSKAT_EXT__?.api;
  if (!api) throw new Error("Extension API not available");
  return api;
}

function action<P, R>(name: string, assetId?: number) {
  return (params: P, onEvent?: EventCallback): Promise<R> => {
    const args = { ...(params as Record<string, unknown>), assetId: assetId || 0 };
    return getAPI().executeAction("oss", name, args, onEvent) as Promise<R>;
  };
}

export function useOSSClient(assetId?: number) {
  const listBuckets = useCallback(action<Record<string, never>, ListBucketsResponse>("list_buckets", assetId), [assetId]);
  const browse = useCallback(action<BrowseParams, BrowseResponse>("browse", assetId), [assetId]);
  const search = useCallback(action<SearchParams, SearchResponse>("search", assetId), [assetId]);
  const preview = useCallback(action<PreviewParams, PreviewResponse>("preview", assetId), [assetId]);
  const upload = useCallback(action<UploadParams, UploadResponse>("upload", assetId), [assetId]);
  const download = useCallback(action<DownloadParams, DownloadResponse>("download", assetId), [assetId]);
  const batchDelete = useCallback(action<BatchDeleteParams, BatchDeleteResponse>("batch_delete", assetId), [assetId]);
  const batchCopy = useCallback(action<BatchCopyParams, BatchCopyResponse>("batch_copy", assetId), [assetId]);
  const presign = useCallback(action<PresignParams, PresignResponse>("get_presigned_url", assetId), [assetId]);

  return useMemo(
    () => ({ listBuckets, browse, search, preview, upload, download, batchDelete, batchCopy, presign }),
    [listBuckets, browse, search, preview, upload, download, batchDelete, batchCopy, presign],
  );
}
