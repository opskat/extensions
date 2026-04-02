// -- Domain objects --

export interface OSSObject {
  key: string;
  size: number;
  lastModified?: string;
  etag?: string;
}

export interface BucketInfo {
  name: string;
  creationDate?: string;
}

// -- Action parameters --

export interface BrowseParams {
  bucket?: string;
  prefix?: string;
  delimiter?: string;
  maxKeys?: number;
  continuationToken?: string;
}

export interface SearchParams {
  bucket?: string;
  prefix?: string;
  maxKeys?: number;
}

export interface PreviewParams {
  bucket?: string;
  key: string;
  maxSize?: number;
}

export interface UploadParams {
  bucket?: string;
  key: string;
  content?: string; // base64 for DevServer/testing
}

export interface DownloadParams {
  bucket?: string;
  key: string;
  savePath?: string;
}

export interface BatchDeleteParams {
  bucket?: string;
  keys: string[];
}

export interface BatchCopyParams {
  bucket?: string;
  keys: string[];
  destBucket?: string;
  destPrefix: string;
}

export interface PresignParams {
  bucket?: string;
  key: string;
  expires?: number;
}

// -- Action responses --

export interface ListBucketsResponse {
  buckets: BucketInfo[];
  defaultBucket: string;
}

export interface BrowseResponse {
  bucket: string;
  prefix: string;
  objects: OSSObject[];
  commonPrefixes: string[];
  isTruncated: boolean;
  nextContinuationToken?: string;
}

export interface SearchResponse {
  bucket: string;
  prefix: string;
  objects: OSSObject[];
}

export interface PreviewResponse {
  key: string;
  contentType: string;
  size: number;
  truncated: boolean;
  encoding: "utf-8" | "base64";
  content: string;
}

export interface UploadResponse {
  key: string;
  bucket: string;
  etag: string;
  size: number;
}

export interface DownloadResponse {
  key: string;
  savedTo: string;
  size: number;
}

export interface BatchDeleteResponse {
  bucket: string;
  deleted: string[];
  deletedCount: number;
  errors?: string[];
  errorCount?: number;
}

export interface BatchCopyResponse {
  srcBucket: string;
  destBucket: string;
  copiedCount: number;
}

export interface PresignResponse {
  url: string;
  expiresIn: number;
}

// -- Progress events --

export interface TransferProgress {
  loaded: number;
  total: number;
}

export interface BatchProgress {
  done: number;
  total: number;
}

// -- Browser state --

/** A unified entry for display: either a "directory" (from commonPrefixes) or a file (from objects). */
export interface FileEntry {
  key: string;
  name: string; // last segment of key (e.g. "photo.jpg" or "subdir/")
  isDir: boolean;
  size: number;
  lastModified?: string;
}

export type ViewMode = "list" | "grid";
