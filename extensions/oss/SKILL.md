# OSS Object Storage

Manage S3-compatible object storage (AWS S3, Aliyun OSS, MinIO).

## Available Tools

- **list_buckets** — List all buckets in the account
- **list_objects** — List objects in a bucket with prefix filter and pagination
- **get_object_info** — Get object metadata (size, content type, last modified)
- **download_object** — Download object content (returned inline for files < 1 MB, saved via dialog for larger)
- **upload_object** — Upload content or file to a bucket
- **copy_object** — Copy an object within or across buckets
- **move_object** — Move an object (copy then delete source)
- **delete_object** — Delete a single object
- **delete_objects** — Batch delete multiple objects
- **presign_url** — Generate a time-limited presigned download URL

## Usage Notes

- If no `bucket` parameter is given, the configured default bucket is used.
- Object keys use `/` as path separator (e.g., `docs/reports/q1.pdf`).
- For large file downloads (> 1 MB) a save dialog is shown automatically.
- `upload_object` accepts inline `content` for small payloads; omit `content` to open a file picker.
- Use `presign_url` to generate temporary public links (default expiry: 1 hour).
